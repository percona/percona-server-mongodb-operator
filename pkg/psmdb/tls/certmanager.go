package tls

import (
	"context"
	"os"
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

func issuerName(cr *api.PerconaServerMongoDB) string {
	const suffix = "-psmdb-issuer"
	tls := cr.Spec.TLS
	switch {
	case tls != nil && tls.IssuerConf.Name != "":
		return tls.IssuerConf.Name
	case tls != nil && tls.IssuerConf.Kind == cm.ClusterIssuerKind:
		return cr.Name + "-" + cr.Namespace + suffix
	}
	return cr.Name + suffix
}

func caIssuerName(cr *api.PerconaServerMongoDB) string {
	const suffix = "-psmdb-ca-issuer"
	if tls := cr.Spec.TLS; tls != nil && tls.IssuerConf.Kind == cm.ClusterIssuerKind {
		return cr.Name + "-" + cr.Namespace + suffix
	}
	return cr.Name + suffix
}

func (c *certManagerController) createOrUpdate(ctx context.Context, cr *api.PerconaServerMongoDB, obj client.Object) (util.ApplyStatus, error) {
	if cr.Namespace == obj.GetNamespace() {
		if err := controllerutil.SetControllerReference(cr, obj, c.scheme); err != nil {
			return "", errors.Wrap(err, "set controller reference")
		}
	}

	status, err := util.Apply(ctx, c.cl, obj)
	if err != nil {
		return "", errors.Wrap(err, "create or update")
	}
	return status, nil
}

func (c *certManagerController) ApplyIssuer(ctx context.Context, cr *api.PerconaServerMongoDB) (util.ApplyStatus, error) {
	var issuer client.Object
	meta := metav1.ObjectMeta{
		Name:      issuerName(cr),
		Namespace: cr.Namespace,
		Labels:    naming.ClusterLabels(cr),
	}
	spec := cm.IssuerSpec{
		IssuerConfig: cm.IssuerConfig{
			CA: &cm.CAIssuer{
				SecretName: CertificateCA(cr).SecretName(),
			},
		},
	}
	switch cr.Spec.TLS.IssuerConf.Kind {
	case cm.IssuerKind:
		issuer = &cm.Issuer{
			ObjectMeta: meta,
			Spec:       spec,
		}
	case cm.ClusterIssuerKind:
		issuer = &cm.ClusterIssuer{
			ObjectMeta: meta,
			Spec:       spec,
		}
	default:
		return "", errors.Errorf("unknown issuer kind: %s", cr.Spec.TLS.IssuerConf.Kind)
	}

	if cr.CompareVersion("1.17.0") < 0 {
		issuer.SetLabels(nil)
	}

	return c.createOrUpdate(ctx, cr, issuer)
}

func (c *certManagerController) ApplyCAIssuer(ctx context.Context, cr *api.PerconaServerMongoDB) (util.ApplyStatus, error) {
	var issuer client.Object
	meta := metav1.ObjectMeta{
		Name:      caIssuerName(cr),
		Namespace: cr.Namespace,
		Labels:    naming.ClusterLabels(cr),
	}
	spec := cm.IssuerSpec{
		IssuerConfig: cm.IssuerConfig{
			SelfSigned: &cm.SelfSignedIssuer{},
		},
	}

	switch cr.Spec.TLS.IssuerConf.Kind {
	case cm.IssuerKind:
		issuer = &cm.Issuer{
			ObjectMeta: meta,
			Spec:       spec,
		}
	case cm.ClusterIssuerKind:
		issuer = &cm.ClusterIssuer{
			ObjectMeta: meta,
			Spec:       spec,
		}
	default:
		return "", errors.Errorf("unknown issuer kind: %s", cr.Spec.TLS.IssuerConf.Kind)
	}

	if cr.CompareVersion("1.17.0") < 0 {
		issuer.SetLabels(nil)
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
					Namespace: cert.Namespace(),
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
					if metav1.IsControlledBy(secret, certificate) || secret.Namespace != cr.Namespace {
						continue
					}
					if err = controllerutil.SetControllerReference(cr, secret, c.scheme); err != nil {
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

func certManagerNamespace() string {
	ns := os.Getenv("CERTMANAGER_NAMESPACE")
	if ns == "" {
		return "cert-manager"
	}
	return ns
}
