package perconaservermongodb

import (
	"context"
	"errors"
	"testing"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

type sslCertManagerController struct {
	tls.CertManagerController
	checks  int
	applies int
	err     error
}

func (c *sslCertManagerController) Check(context.Context, *rest.Config, string) error {
	c.checks++
	return c.err
}

func (c *sslCertManagerController) ApplyIssuer(context.Context, *api.PerconaServerMongoDB) (util.ApplyStatus, error) {
	c.applies++
	return "", c.err
}

func (c *sslCertManagerController) ApplyCAIssuer(context.Context, *api.PerconaServerMongoDB) (util.ApplyStatus, error) {
	c.applies++
	return "", c.err
}

func TestReconcileSSLCertManagerCheck(t *testing.T) {
	for _, tt := range []struct {
		name                            string
		external, internal, certManager bool
		checks, applies                 int
	}{
		{name: "existing manual secrets", external: true, internal: true},
		{name: "existing cert-manager secrets", external: true, internal: true, certManager: true, applies: 1},
		{name: "missing external secret", internal: true, checks: 1},
		{name: "missing internal secret", external: true, checks: 1},
		{name: "missing both secrets", checks: 1},
		{name: "missing cert-manager internal secret", external: true, certManager: true, checks: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cr, err := readDefaultCR("cluster", "namespace")
			require.NoError(t, err)
			cr.UID = "cluster-uid"
			cr.Spec.Secrets.SSL = cr.Name + "-ssl"
			cr.Spec.Secrets.SSLInternal = cr.Name + "-ssl-internal"
			controller := true
			var objects []client.Object
			for name, exists := range map[string]bool{
				api.SSLSecretName(cr):         tt.external,
				api.SSLInternalSecretName(cr): tt.internal,
			} {
				if !exists {
					continue
				}
				secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
					Name: name, Namespace: cr.Namespace,
					OwnerReferences: []metav1.OwnerReference{{UID: cr.UID, Controller: &controller}},
				}}
				if tt.certManager {
					secret.Labels = map[string]string{cm.PartOfCertManagerControllerLabelKey: "true"}
				}
				objects = append(objects, secret)
			}
			r := buildFakeClient(objects...)
			stop := errors.New("stop cert-manager operation")
			c := &sslCertManagerController{err: stop}
			r.newCertManagerCtrlFunc = func(client.Client, *runtime.Scheme, bool) tls.CertManagerController {
				return c
			}
			err = r.reconcileSSL(t.Context(), cr)
			if tt.checks > 0 || tt.applies > 0 {
				require.ErrorIs(t, err, stop)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.checks, c.checks)
			require.Equal(t, tt.applies, c.applies)
		})
	}
}
