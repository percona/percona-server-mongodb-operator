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

type fakeCertManagerController struct {
	dryRun bool
	cl     client.Client
}

var _ tls.CertManagerController = new(fakeCertManagerController)

func NewCertManagerController(cl client.Client, scheme *runtime.Scheme, dryRun bool) tls.CertManagerController {
	return &fakeCertManagerController{
		dryRun: dryRun,
		cl:     cl,
	}
}

func (c *fakeCertManagerController) ApplyIssuer(ctx context.Context, cr *api.PerconaServerMongoDB) (util.ApplyStatus, error) {
	return util.ApplyStatusUnchanged, nil
}

func (c *fakeCertManagerController) ApplyCAIssuer(ctx context.Context, cr *api.PerconaServerMongoDB) (util.ApplyStatus, error) {
	return util.ApplyStatusUnchanged, nil
}

func (c *fakeCertManagerController) ApplyCertificate(ctx context.Context, cr *api.PerconaServerMongoDB, internal bool) (util.ApplyStatus, error) {
	return util.ApplyStatusUnchanged, nil
}

func (c *fakeCertManagerController) ApplyCACertificate(ctx context.Context, cr *api.PerconaServerMongoDB) (util.ApplyStatus, error) {
	return util.ApplyStatusUnchanged, nil
}

func (c *fakeCertManagerController) DeleteDeprecatedIssuerIfExists(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	return nil
}

func (c *fakeCertManagerController) WaitForCerts(ctx context.Context, cr *api.PerconaServerMongoDB, secretsList ...string) error {
	return nil
}

func (c *fakeCertManagerController) GetMergedCA(ctx context.Context, cr *api.PerconaServerMongoDB, secretNames []string) ([]byte, error) {
	return nil, nil
}

func (c *fakeCertManagerController) Check(ctx context.Context, config *rest.Config, ns string) error {
	return tls.ErrCertManagerNotFound
}

func (c *fakeCertManagerController) IsDryRun() bool {
	return false
}

func (c *fakeCertManagerController) GetClient() client.Client {
	return c.cl
}
