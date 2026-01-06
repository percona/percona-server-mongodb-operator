package tls

import (
	"context"
	"regexp"
	"time"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/cert-manager/cert-manager/pkg/util/cmapichecker"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

type CertManagerController interface {
	ApplyIssuer(ctx context.Context, cr *api.PerconaServerMongoDB) (util.ApplyStatus, error)
	ApplyCAIssuer(ctx context.Context, cr *api.PerconaServerMongoDB) (util.ApplyStatus, error)
	ApplyCertificate(ctx context.Context, cr *api.PerconaServerMongoDB, cert Certificate) (util.ApplyStatus, error)
	DeleteDeprecatedIssuerIfExists(ctx context.Context, cr *api.PerconaServerMongoDB) error
	WaitForCerts(ctx context.Context, cr *api.PerconaServerMongoDB, certificates ...Certificate) error
	GetMergedCA(ctx context.Context, cr *api.PerconaServerMongoDB, secretNames []string) ([]byte, error)
	Check(ctx context.Context, config *rest.Config, ns string) error
	IsDryRun() bool
	GetClient() client.Client
}

type certManagerController struct {
	cl     client.Client
	scheme *runtime.Scheme
	dryRun bool
}

var _ CertManagerController = new(certManagerController)

type NewCertManagerControllerFunc func(cl client.Client, scheme *runtime.Scheme, dryRun bool) CertManagerController

func NewCertManagerController(cl client.Client, scheme *runtime.Scheme, dryRun bool) CertManagerController {
	if dryRun {
		cl = client.NewDryRunClient(cl)
	}
	return &certManagerController{
		cl:     cl,
		scheme: scheme,
		dryRun: dryRun,
	}
}

func (c *certManagerController) IsDryRun() bool {
	return c.dryRun
}

func deprecatedIssuerName(cr *api.PerconaServerMongoDB) string {
	return cr.Name + "-psmdb-ca"
}

func issuerName(cr *api.PerconaServerMongoDB) string {
	if cr.CompareVersion("1.16.0") >= 0 && cr.Spec.TLS != nil && cr.Spec.TLS.IssuerConf != nil {
		return cr.Spec.TLS.IssuerConf.Name
	}

	if cr.CompareVersion("1.15.0") < 0 {
		return deprecatedIssuerName(cr)
	}

	return cr.Name + "-psmdb-issuer"
}

func caIssuerName(cr *api.PerconaServerMongoDB) string {
	return cr.Name + "-psmdb-ca-issuer"
}

func (c *certManagerController) DeleteDeprecatedIssuerIfExists(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	issuer := new(cm.Issuer)
	err := c.cl.Get(ctx, types.NamespacedName{
		Name:      deprecatedIssuerName(cr),
		Namespace: cr.Namespace,
	}, issuer)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return c.cl.Delete(ctx, issuer)
}

func (c *certManagerController) createOrUpdate(ctx context.Context, cr *api.PerconaServerMongoDB, obj client.Object) (util.ApplyStatus, error) {
	if err := controllerutil.SetControllerReference(cr, obj, c.scheme); err != nil {
		return "", errors.Wrap(err, "set controller reference")
	}

	status, err := util.Apply(ctx, c.cl, obj)
	if err != nil {
		return "", errors.Wrap(err, "create or update")
	}
	return status, nil
}

func (c *certManagerController) ApplyIssuer(ctx context.Context, cr *api.PerconaServerMongoDB) (util.ApplyStatus, error) {
	issuer := &cm.Issuer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      issuerName(cr),
			Namespace: cr.Namespace,
			Labels:    naming.ClusterLabels(cr),
		},
		Spec: cm.IssuerSpec{
			IssuerConfig: cm.IssuerConfig{
				CA: &cm.CAIssuer{
					SecretName: CertificateCA(cr).SecretName(),
				},
			},
		},
	}

	if cr.CompareVersion("1.15.0") < 0 {
		issuer.Spec = cm.IssuerSpec{
			IssuerConfig: cm.IssuerConfig{
				SelfSigned: &cm.SelfSignedIssuer{},
			},
		}
	}

	if cr.CompareVersion("1.17.0") < 0 {
		issuer.Labels = nil
	}

	return c.createOrUpdate(ctx, cr, issuer)
}

func (c *certManagerController) ApplyCAIssuer(ctx context.Context, cr *api.PerconaServerMongoDB) (util.ApplyStatus, error) {
	issuer := &cm.Issuer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      caIssuerName(cr),
			Namespace: cr.Namespace,
			Labels:    naming.ClusterLabels(cr),
		},
		Spec: cm.IssuerSpec{
			IssuerConfig: cm.IssuerConfig{
				SelfSigned: &cm.SelfSignedIssuer{},
			},
		},
	}

	if cr.CompareVersion("1.17.0") < 0 {
		issuer.Labels = nil
	}

	return c.createOrUpdate(ctx, cr, issuer)
}

func (c *certManagerController) ApplyCertificate(ctx context.Context, cr *api.PerconaServerMongoDB, cert Certificate) (util.ApplyStatus, error) {
	return c.createOrUpdate(ctx, cr, cert.Object())
}

var (
	ErrCertManagerNotFound = errors.New("cert-manager not found")
	ErrCertManagerNotReady = errors.New("cert-manager not ready")
)

func (c *certManagerController) Check(ctx context.Context, config *rest.Config, ns string) error {
	log := logf.FromContext(ctx)
	checker, err := cmapichecker.New(config, ns)
	if err != nil {
		return err
	}
	err = checker.Check(ctx)
	if err != nil {
		switch err := translateCheckError(err); {
		case errors.Is(err, cmapichecker.ErrCertManagerCRDsNotFound):
			return ErrCertManagerNotFound
		case errors.Is(err, cmapichecker.ErrWebhookCertificateFailure), errors.Is(err, cmapichecker.ErrWebhookServiceFailure), errors.Is(err, cmapichecker.ErrWebhookDeploymentFailure):
			log.Error(cmapichecker.TranslateToSimpleError(err), "cert-manager is not ready")
			return ErrCertManagerNotReady
		}
		return err
	}
	return nil
}

func translateCheckError(err error) error {
	const crdsMapping3Error = `error finding the scope of the object: failed to get restmapping: unable to retrieve the complete list of server APIs: cert-manager.io/v1: no matches for cert-manager.io/v1, Resource=`
	// TODO: remove as soon as TranslateToSimpleError uses this regexp
	regexErrCertManagerCRDsNotFound := regexp.MustCompile(`^(` + regexp.QuoteMeta(crdsMapping3Error) + `)$`)

	if regexErrCertManagerCRDsNotFound.MatchString(err.Error()) {
		return cmapichecker.ErrCertManagerCRDsNotFound
	}

	return cmapichecker.TranslateToSimpleError(err)
}

func (c *certManagerController) WaitForCerts(ctx context.Context, cr *api.PerconaServerMongoDB, certificates ...Certificate) error {
	if c.dryRun {
		return nil
	}
	ticker := time.NewTicker(1 * time.Second)
	timeoutTimer := time.NewTimer(30 * time.Second)
	defer timeoutTimer.Stop()
	defer ticker.Stop()
	for {
		select {
		case <-timeoutTimer.C:
			return errors.Errorf("timeout: can't get tls certificates from certmanager, %v", certificates)
		case <-ticker.C:
			successCount := 0
			for _, cert := range certificates {
				secret := &corev1.Secret{}
				err := c.cl.Get(ctx, types.NamespacedName{
					Name:      cert.SecretName(),
					Namespace: cr.Namespace,
				}, secret)
				if err != nil && !k8serrors.IsNotFound(err) {
					return err
				} else if err == nil {
					successCount++
					if v, ok := secret.Annotations[cm.CertificateNameKey]; !ok || v != cert.Name() {
						continue
					}
					certificate := &cm.Certificate{}
					err := c.cl.Get(ctx, client.ObjectKeyFromObject(cert.Object()), certificate)
					if err != nil {
						return err
					}
					if metav1.IsControlledBy(secret, certificate) {
						continue
					}
					if err = controllerutil.SetControllerReference(cr, secret, c.scheme); err != nil {
						var alreadyOwnedErr *controllerutil.AlreadyOwnedError
						if errors.As(err, &alreadyOwnedErr) {
							continue
						}
						return errors.Wrap(err, "set controller reference")
					}
					if err = c.cl.Update(ctx, secret); err != nil {
						return errors.Wrap(err, "failed to update secret")
					}
				}
			}
			if successCount == len(certificates) {
				return nil
			}
		}
	}
}

// GetMergedCA returns merged CA from provided secrets. Result will not contain PEM duplicates.
func (c *certManagerController) GetMergedCA(ctx context.Context, cr *api.PerconaServerMongoDB, secretNames []string) ([]byte, error) {
	mergedCA := []byte{}

	for _, secretName := range secretNames {
		secret := new(corev1.Secret)
		err := c.cl.Get(ctx, types.NamespacedName{
			Name:      secretName,
			Namespace: cr.Namespace,
		}, secret)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			return nil, errors.Wrap(err, "get old ssl secret")
		}
		if len(mergedCA) == 0 {
			mergedCA = secret.Data["ca.crt"]
			continue
		}

		mergedCA, err = MergePEM(mergedCA, secret.Data["ca.crt"])
		if err != nil {
			return nil, errors.Wrap(err, "merge old ssl and ssl internal secret")
		}
	}
	return mergedCA, nil
}

func (c *certManagerController) GetClient() client.Client {
	return c.cl
}
