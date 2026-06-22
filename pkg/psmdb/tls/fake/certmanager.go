package fake

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

type CertManagerController struct {
	DryRun bool
	Client client.Client

	ApplyIssuerCalls      int
	ApplyCAIssuerCalls    int
	ApplyCertificateCalls int
	WaitForCertsCalls     int
	CertNames             []string
	IssuerRefNames        []string
	IssuerRefKinds        []string
	IssuerRefGroups       []string
	WaitForCertNames      [][]string
}

var _ tls.CertManagerController = new(CertManagerController)

func NewCertManagerController(cl client.Client, scheme *runtime.Scheme, dryRun bool) tls.CertManagerController {
	return &CertManagerController{
		DryRun: dryRun,
		Client: cl,
	}
}

func (c *CertManagerController) ApplyIssuer(ctx context.Context, cr *api.PerconaServerMongoDB) (util.ApplyStatus, error) {
	c.ApplyIssuerCalls++
	return util.ApplyStatusUnchanged, nil
}

func (c *CertManagerController) ApplyCAIssuer(ctx context.Context, cr *api.PerconaServerMongoDB) (util.ApplyStatus, error) {
	c.ApplyCAIssuerCalls++
	return util.ApplyStatusUnchanged, nil
}

func (c *CertManagerController) ApplyCertificate(ctx context.Context, cr *api.PerconaServerMongoDB, cert tls.Certificate) (util.ApplyStatus, error) {
	c.ApplyCertificateCalls++
	obj := cert.Object()
	c.CertNames = append(c.CertNames, cert.Name())
	c.IssuerRefNames = append(c.IssuerRefNames, obj.Spec.IssuerRef.Name)
	c.IssuerRefKinds = append(c.IssuerRefKinds, obj.Spec.IssuerRef.Kind)
	c.IssuerRefGroups = append(c.IssuerRefGroups, obj.Spec.IssuerRef.Group)
	return util.ApplyStatusUnchanged, nil
}

func (c *CertManagerController) DeleteDeprecatedIssuerIfExists(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	return nil
}

func (c *CertManagerController) WaitForCerts(ctx context.Context, cr *api.PerconaServerMongoDB, cert ...tls.Certificate) error {
	c.WaitForCertsCalls++
	names := make([]string, 0, len(cert))
	for _, cert := range cert {
		names = append(names, cert.Name())
	}
	c.WaitForCertNames = append(c.WaitForCertNames, names)
	return nil
}

func (c *CertManagerController) GetMergedCA(ctx context.Context, cr *api.PerconaServerMongoDB, secretNames []string) ([]byte, error) {
	return nil, nil
}

func (c *CertManagerController) Check(ctx context.Context, config *rest.Config, ns string) error {
	return tls.ErrCertManagerNotFound
}

func (c *CertManagerController) IsDryRun() bool {
	return c.DryRun
}

func (c *CertManagerController) GetClient() client.Client {
	return c.Client
}
