package perconaservermongodb

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestReconcilePMMSidecar(t *testing.T) {
	t.Parallel()

	const ns = "pmm-sidecar"

	tests := map[string]struct {
		usersData map[string][]byte
		want      bool
	}{
		"users secret has PMM_SERVER_TOKEN": {
			usersData: map[string][]byte{api.PMMServerToken: []byte("token")},
			want:      true,
		},
		"users secret without PMM_SERVER_TOKEN": {
			usersData: map[string][]byte{"MONGODB_BACKUP_USER": []byte("backup")},
			want:      false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()

			cr, err := readDefaultCR("some-cluster", ns)
			require.NoError(t, err)
			cr.Spec.PMM.Enabled = true
			require.NoError(t, cr.CheckNSetDefaults(ctx, version.PlatformKubernetes))
			// mongos is reconciled only once every cfg pod exists
			cr.Spec.Sharding.ConfigsvrReplSet.Size = 0

			caCert, tlsCert, tlsKey, err := tls.Issue(tls.GetCertificateSans(cr))
			require.NoError(t, err)

			r := buildFakeClient(cr,
				&corev1.Secret{Name: api.UserSecretName(cr), Namespace: ns, Data: tt.usersData},
				&corev1.Secret{
					Name:      api.SSLSecretName(cr),
					Namespace: ns,
					Data:      map[string][]byte{"ca.crt": caCert, "tls.crt": tlsCert, "tls.key": tlsKey},
				},
				&corev1.Secret{Name: api.SSLInternalSecretName(cr), Namespace: ns},
				&corev1.Secret{Name: tls.CertificatePBMClient(cr).SecretName(), Namespace: ns},
			)

			hasPMM := func(sts *appsv1.StatefulSet) bool {
				return slices.ContainsFunc(sts.Spec.Template.Spec.Containers, func(c corev1.Container) bool {
					return c.Name == "pmm-client"
				})
			}

			for _, rs := range []*api.ReplsetSpec{cr.Spec.Replsets[0], cr.Spec.Sharding.ConfigsvrReplSet} {
				sts, err := r.reconcileStatefulSet(ctx, cr, rs, naming.MongodLabels(cr, rs))
				require.NoError(t, err)
				assert.Equal(t, tt.want, hasPMM(sts), rs.Name)
			}

			require.NoError(t, r.reconcileMongosStatefulset(ctx, cr))
			mongos := psmdb.MongosStatefulset(cr)
			require.NoError(t, r.client.Get(ctx, client.ObjectKeyFromObject(mongos), mongos))
			assert.Equal(t, tt.want, hasPMM(mongos), "mongos")
		})
	}
}
