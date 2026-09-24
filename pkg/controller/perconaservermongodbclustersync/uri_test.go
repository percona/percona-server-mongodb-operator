package perconaservermongodbclustersync

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func TestBuildTargetURI(t *testing.T) {
	tests := map[string]struct {
		target   *psmdbv1.PerconaServerMongoDB
		username string
		password string
		want     string
		wantErr  string
	}{
		"replicaset target": {
			target: &psmdbv1.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Namespace: "ns"},
				Spec: psmdbv1.PerconaServerMongoDBSpec{
					Replsets: []*psmdbv1.ReplsetSpec{{Name: "rs0"}},
				},
			},
			username: "sync",
			password: "syncpass",
			want:     "mongodb://sync:syncpass@cluster1-rs0.ns.svc.cluster.local:27017/?replicaSet=rs0",
		},
		"replicaset target with custom dns suffix": {
			target: &psmdbv1.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Namespace: "ns"},
				Spec: psmdbv1.PerconaServerMongoDBSpec{
					ClusterServiceDNSSuffix: psmdbv1.MultiClusterDefaultDNSSuffix,
					Replsets:                []*psmdbv1.ReplsetSpec{{Name: "rs0"}},
				},
			},
			username: "sync",
			password: "syncpass",
			want:     "mongodb://sync:syncpass@cluster1-rs0.ns.svc.clusterset.local:27017/?replicaSet=rs0",
		},
		"sharded target points at mongos without replicaSet": {
			target: &psmdbv1.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Namespace: "ns"},
				Spec: psmdbv1.PerconaServerMongoDBSpec{
					Replsets: []*psmdbv1.ReplsetSpec{{Name: "rs0"}},
					Sharding: psmdbv1.Sharding{
						Enabled: true,
						Mongos:  &psmdbv1.MongosSpec{Port: 27019},
					},
				},
			},
			username: "sync",
			password: "syncpass",
			want:     "mongodb://sync:syncpass@cluster1-mongos.ns.svc.cluster.local:27019/",
		},
		"credentials are percent-encoded": {
			target: &psmdbv1.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Namespace: "ns"},
				Spec: psmdbv1.PerconaServerMongoDBSpec{
					Replsets: []*psmdbv1.ReplsetSpec{{Name: "rs0"}},
				},
			},
			username: "sync",
			password: "p@ss/word",
			want:     "mongodb://sync:p%40ss%2Fword@cluster1-rs0.ns.svc.cluster.local:27017/?replicaSet=rs0",
		},
		"empty credentials": {
			target:  &psmdbv1.PerconaServerMongoDB{},
			wantErr: "syncTargetUser credentials are empty",
		},
		"no replsets": {
			target: &psmdbv1.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Namespace: "ns"},
			},
			username: "sync",
			password: "syncpass",
			wantErr:  "has no replsets",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := buildTargetURI(tc.target, tc.username, tc.password)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			require.NoError(t, options.Client().ApplyURI(got).Validate())
		})
	}
}

func TestBuildSourceURI(t *testing.T) {
	tests := map[string]struct {
		uri     string
		secret  *corev1.Secret
		want    string
		wantErr string
	}{
		"credentials are injected": {
			uri:    "mongodb://source-rs0.ns.svc.cluster.local:27017/?replicaSet=rs0",
			secret: sourceSecret("src", "srcpass"),
			want:   "mongodb://src:srcpass@source-rs0.ns.svc.cluster.local:27017/?replicaSet=rs0",
		},
		"missing slash before query is normalized": {
			uri:    "mongodb://source-rs0.ns.svc.cluster.local:27017?replicaSet=rs0",
			secret: sourceSecret("src", "srcpass"),
			want:   "mongodb://src:srcpass@source-rs0.ns.svc.cluster.local:27017/?replicaSet=rs0",
		},
		"credentials are percent-encoded": {
			uri:    "mongodb://source-rs0.ns.svc.cluster.local:27017/",
			secret: sourceSecret("src", "p@ss/word"),
			want:   "mongodb://src:p%40ss%2Fword@source-rs0.ns.svc.cluster.local:27017/",
		},
		"missing password": {
			uri:     "mongodb://source-rs0.ns.svc.cluster.local:27017/",
			secret:  sourceSecret("src", ""),
			wantErr: "missing username/password keys",
		},
		"missing secret": {
			uri:     "mongodb://source-rs0.ns.svc.cluster.local:27017/",
			wantErr: "get source credentials secret",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(s))
			require.NoError(t, psmdbv1.SchemeBuilder.AddToScheme(s))

			b := fake.NewClientBuilder().WithScheme(s)
			if tc.secret != nil {
				b = b.WithObjects(tc.secret)
			}

			cr := &psmdbv1.PerconaServerMongoDBClusterSync{
				ObjectMeta: metav1.ObjectMeta{Name: "sync", Namespace: "ns"},
				Spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{
					Source: psmdbv1.ClusterSyncSource{URI: tc.uri, CredentialsSecret: "source-creds"},
				},
			}

			got, err := buildSourceURI(t.Context(), b.Build(), cr)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			require.NoError(t, options.Client().ApplyURI(got).Validate())
		})
	}
}

func sourceSecret(username, password string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "source-creds", Namespace: "ns"},
		Data: map[string][]byte{
			"username": []byte(username),
			"password": []byte(password),
		},
	}
}
