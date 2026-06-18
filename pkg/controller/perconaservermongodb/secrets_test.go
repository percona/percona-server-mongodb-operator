package perconaservermongodb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

func TestEnsureConnectionStringSecret(t *testing.T) {
	tests := map[string]struct {
		setup           func(*api.PerconaServerMongoDB) []client.Object
		includeReplsets bool
		expected        map[string][]byte
	}{
		"replset": {
			includeReplsets: true,
			setup: func(cr *api.PerconaServerMongoDB) []client.Object {
				rs := cr.Spec.Replsets[0]
				return []client.Object{
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "app-user-conn-str",
							Namespace: cr.Namespace,
						},
						Data: map[string][]byte{"stale": []byte("value")},
					},
					fakeStatefulset(cr, rs, rs.Size, "", "mongod"),
					fakePodsForRS(cr, rs)[0],
				}
			},
			expected: map[string][]byte{
				"app_user_rs0_connectionString":    []byte("mongodb://app-user:p%40ss%2Fword@cluster-rs0-0.cluster-rs0.database.svc.cluster.local:27017/?authSource=application&replicaSet=rs0"),
				"app_user_rs0_connectionStringSrv": []byte("mongodb+srv://app-user:p%40ss%2Fword@cluster-rs0.database.svc.cluster.local/?authSource=application&replicaSet=rs0"),
			},
		},
		"exposed replset": {
			includeReplsets: true,
			setup: func(cr *api.PerconaServerMongoDB) []client.Object {
				rs := cr.Spec.Replsets[0]
				rs.Expose.Enabled = true
				pod := fakePodsForRS(cr, rs)[0]
				return []client.Object{
					fakeStatefulset(cr, rs, rs.Size, "", "mongod"),
					pod,
					&corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pod.GetName(),
							Namespace: cr.Namespace,
						},
						Spec: corev1.ServiceSpec{
							Type: corev1.ServiceTypeLoadBalancer,
							Ports: []corev1.ServicePort{
								{Name: "mongodb", Port: 27017},
							},
						},
						Status: corev1.ServiceStatus{
							LoadBalancer: corev1.LoadBalancerStatus{
								Ingress: []corev1.LoadBalancerIngress{
									{Hostname: "rs0.example.com"},
								},
							},
						},
					},
				}
			},
			expected: map[string][]byte{
				"app_user_rs0_connectionString":        []byte("mongodb://app-user:p%40ss%2Fword@cluster-rs0-0.cluster-rs0.database.svc.cluster.local:27017/?authSource=application&replicaSet=rs0"),
				"app_user_rs0_connectionStringSrv":     []byte("mongodb+srv://app-user:p%40ss%2Fword@cluster-rs0.database.svc.cluster.local/?authSource=application&replicaSet=rs0"),
				"app_user_rs0_connectionStringExposed": []byte("mongodb://app-user:p%40ss%2Fword@rs0.example.com:27017/?authSource=application&replicaSet=rs0"),
			},
		},
		"mongos without replsets": {
			setup: func(cr *api.PerconaServerMongoDB) []client.Object {
				cr.Spec.Sharding = api.Sharding{
					Enabled: true,
					Mongos:  &api.MongosSpec{},
				}
				return []client.Object{
					&corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name:      naming.MongosServiceName(cr),
							Namespace: cr.Namespace,
						},
						Spec: corev1.ServiceSpec{
							Ports: []corev1.ServicePort{
								{Name: "mongos", Port: 27017},
							},
						},
					},
				}
			},
			expected: map[string][]byte{
				"app_user_mongos_connectionString": []byte("mongodb://app-user:p%40ss%2Fword@cluster-mongos.database.svc.cluster.local:27017/?authSource=application"),
			},
		},
		"exposed mongos": {
			setup: func(cr *api.PerconaServerMongoDB) []client.Object {
				cr.Spec.Sharding = api.Sharding{
					Enabled: true,
					Mongos: &api.MongosSpec{
						Size: 1,
						Expose: api.MongosExpose{
							ServicePerPod: true,
							Expose: api.Expose{
								ExposeType: corev1.ServiceTypeLoadBalancer,
							},
						},
					},
				}
				pod := fakePodsForMongos(cr)[0]
				return []client.Object{
					pod,
					&corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Name:      pod.GetName(),
							Namespace: cr.Namespace,
						},
						Spec: corev1.ServiceSpec{
							Type:      corev1.ServiceTypeLoadBalancer,
							ClusterIP: "10.0.0.20",
							Ports: []corev1.ServicePort{
								{Name: "mongos", Port: 27017},
							},
						},
						Status: corev1.ServiceStatus{
							LoadBalancer: corev1.LoadBalancerStatus{
								Ingress: []corev1.LoadBalancerIngress{
									{Hostname: "mongos.example.com"},
								},
							},
						},
					},
				}
			},
			expected: map[string][]byte{
				"app_user_mongos_connectionString":        []byte("mongodb://app-user:p%40ss%2Fword@10.0.0.20/?authSource=application"),
				"app_user_mongos_connectionStringExposed": []byte("mongodb://app-user:p%40ss%2Fword@mongos.example.com/?authSource=application"),
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := connectionStringTestCluster()
			owner := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app-user-password",
					Namespace: cr.Namespace,
					UID:       types.UID("owner-uid"),
				},
			}
			objects := append([]client.Object{owner}, tt.setup(cr)...)
			r := buildFakeClient(objects...)

			err := ensureConnectionStringSecret(
				t.Context(),
				r.client,
				cr,
				"app-user-conn-str",
				"app/user",
				psmdb.Credentials{
					Username:   "app-user",
					Password:   "p@ss/word",
					AuthSource: "application",
				},
				owner,
				tt.includeReplsets,
			)
			require.NoError(t, err)

			actual := new(corev1.Secret)
			require.NoError(t, r.client.Get(t.Context(), types.NamespacedName{
				Name:      "app-user-conn-str",
				Namespace: cr.Namespace,
			}, actual))
			assert.Equal(t, tt.expected, actual.Data)
			for key := range actual.Data {
				assert.Empty(t, validation.IsConfigMapKey(key))
			}
			require.Len(t, actual.OwnerReferences, 1)
			assert.Equal(t, owner.UID, actual.OwnerReferences[0].UID)
			assert.Equal(t, "Secret", actual.OwnerReferences[0].Kind)
		})
	}
}

func TestReconcileUsersCreatesConnectionStringSecretWhenCredentialsUnchanged(t *testing.T) {
	cr := connectionStringTestCluster()
	cr.Status.State = api.AppStateReady
	cr.Spec.Secrets = &api.SecretsSpec{Users: "cluster-users"}

	users := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Spec.Secrets.Users,
			Namespace: cr.Namespace,
			UID:       types.UID("users-secret-uid"),
		},
		Data: map[string][]byte{
			api.EnvMongoDBDatabaseAdminUser:     []byte("databaseAdmin"),
			api.EnvMongoDBDatabaseAdminPassword: []byte("password"),
		},
	}
	internal := users.DeepCopy()
	internal.Name = api.InternalUserSecretName(cr)
	internal.UID = types.UID("internal-secret-uid")
	internal.Data = getInternalSecretData(cr, users)

	rs := cr.Spec.Replsets[0]
	r := buildFakeClient(
		users,
		internal,
		fakeStatefulset(cr, rs, rs.Size, "", "mongod"),
		fakePodsForRS(cr, rs)[0],
	)

	require.NoError(t, r.reconcileUsers(t.Context(), cr, cr.Spec.Replsets))

	actual := new(corev1.Secret)
	key := types.NamespacedName{
		Name:      naming.SecretDatabaseAdminConnStrName(cr),
		Namespace: cr.Namespace,
	}
	require.NoError(t, r.client.Get(t.Context(), key, actual))
	assert.Contains(t, actual.Data, "databaseAdmin_rs0_connectionString")
}

func TestEnsureCustomUsersConnectionStringSecretsIncludesMultipleDefaultUsers(t *testing.T) {
	cr := connectionStringTestCluster()
	users := []api.User{
		{Name: "app-user", DB: "application", Roles: []api.UserRole{{Name: "readWrite", DB: "application"}}},
		{Name: "report-user", DB: "reports", Roles: []api.UserRole{{Name: "read", DB: "reports"}}},
	}
	owner := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      users[0].DefaultSecretName(cr),
			Namespace: cr.Namespace,
			UID:       types.UID("custom-users-secret-uid"),
		},
		Data: map[string][]byte{
			"app-user":    []byte("p@ss/word"),
			"report-user": []byte("report/pass"),
		},
	}
	rs := cr.Spec.Replsets[0]
	r := buildFakeClient(
		owner,
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      naming.SecretCustomUserConnStrName(cr, &users[0]),
				Namespace: cr.Namespace,
			},
			Data: map[string][]byte{"stale": []byte("value")},
		},
		fakeStatefulset(cr, rs, rs.Size, "", "mongod"),
		fakePodsForRS(cr, rs)[0],
	)

	require.NoError(t, ensureCustomUsersConnectionStringSecrets(t.Context(), r.client, cr, users))

	actual := new(corev1.Secret)
	require.NoError(t, r.client.Get(t.Context(), types.NamespacedName{
		Name:      naming.SecretCustomUserConnStrName(cr, &users[0]),
		Namespace: cr.Namespace,
	}, actual))
	assert.Equal(t, map[string][]byte{
		"app-user_rs0_connectionString":       []byte("mongodb://app-user:p%40ss%2Fword@cluster-rs0-0.cluster-rs0.database.svc.cluster.local:27017/?authSource=application&replicaSet=rs0"),
		"app-user_rs0_connectionStringSrv":    []byte("mongodb+srv://app-user:p%40ss%2Fword@cluster-rs0.database.svc.cluster.local/?authSource=application&replicaSet=rs0"),
		"report-user_rs0_connectionString":    []byte("mongodb://report-user:report%2Fpass@cluster-rs0-0.cluster-rs0.database.svc.cluster.local:27017/?authSource=reports&replicaSet=rs0"),
		"report-user_rs0_connectionStringSrv": []byte("mongodb+srv://report-user:report%2Fpass@cluster-rs0.database.svc.cluster.local/?authSource=reports&replicaSet=rs0"),
	}, actual.Data)
	require.Len(t, actual.OwnerReferences, 1)
	assert.Equal(t, owner.UID, actual.OwnerReferences[0].UID)
}

func connectionStringTestCluster() *api.PerconaServerMongoDB {
	return &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster",
			Namespace: "database",
		},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion:               "1.20.0",
			ClusterServiceDNSMode:   api.DNSModeInternal,
			ClusterServiceDNSSuffix: "svc.cluster.local",
			TLS:                     &api.TLSSpec{Mode: api.TLSModeDisabled},
			Replsets: []*api.ReplsetSpec{
				{
					Name: "rs0",
					Size: 1,
				},
			},
		},
	}
}
