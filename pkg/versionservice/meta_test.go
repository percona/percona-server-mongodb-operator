package versionservice

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/percona/percona-backup-mongodb/pbm/defs"

	"github.com/percona/percona-server-mongodb-operator/pkg/apis"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func fakeVolumeSpec(t *testing.T) *api.VolumeSpec {
	t.Helper()

	return &api.VolumeSpec{
		PersistentVolumeClaim: api.PVCSpec{
			PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		},
	}
}

func fakeOperatorDeployment(helmDeploy bool) *appsv1.Deployment {
	const operatorName = "percona-server-mongodb-operator"

	size := int32(1)
	labels := make(map[string]string)
	if helmDeploy {
		labels["helm.sh/chart"] = operatorName
	}

	return &appsv1.Deployment{
		Name:   operatorName,
		Labels: labels,
		Spec: appsv1.DeploymentSpec{
			Replicas: &size,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": operatorName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name": operatorName,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: operatorName,
					Containers: []corev1.Container{
						{
							Name: operatorName,
						},
					},
				},
			},
		},
	}
}

func TestBuildMeta(t *testing.T) {
	ctx := t.Context()
	tests := map[string]struct {
		cr              api.PerconaServerMongoDB
		want            Meta
		wantErrMsg      string
		fcv             string
		clusterWide     bool
		helmDeploy      bool
		namespace       string
		watchNamespaces string
		extraObjects    []client.Object
	}{
		"Minimal CR": {
			cr: api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name: "some-name",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Image: "percona/percona-server-mongodb:5.0.11-10",
					Replsets: []*api.ReplsetSpec{
						{
							Name:       "rs0",
							Size:       3,
							VolumeSpec: fakeVolumeSpec(t),
						},
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Size: 3,
				},
			},
			want: Meta{
				Apply:             "disabled",
				Version:           version.Version(),
				ClusterSize:       3,
				EncryptionEnabled: true,
				TLSMode:           string(api.TLSModePrefer),
			},
			namespace: "test-namespace",
		},
		"Full CR with old Version deployed with Helm": {
			cr: api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name: "some-name",
					Labels: map[string]string{
						"helm.sh/chart": "psmdb-db-1.13.0",
					},
				},
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: "1.13.0",
					Image:     "percona/percona-server-mongodb:5.0.11-10",
					Replsets: []*api.ReplsetSpec{
						{
							Name:       "rs0",
							Size:       3,
							VolumeSpec: fakeVolumeSpec(t),
							Sidecars: []corev1.Container{
								{
									Name: "sidecar",
								},
							},
						},
					},
					Backup: api.BackupSpec{
						Enabled: true,
						Storages: map[string]api.BackupStorageSpec{
							"minio": {},
						},
						PITR: api.PITRSpec{
							Enabled: true,
						},
						Tasks: []api.BackupTaskSpec{
							{
								Name:    "test",
								Type:    defs.PhysicalBackup,
								Enabled: true,
							},
						},
					},
					Secrets: &api.SecretsSpec{
						Vault: "vault-secret",
					},
					Sharding: api.Sharding{
						Enabled: true,
						ConfigsvrReplSet: &api.ReplsetSpec{
							VolumeSpec: fakeVolumeSpec(t),
						},
						Mongos: &api.MongosSpec{},
					},
					PMM: api.PMMSpec{
						Enabled: true,
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Size: 2,
				},
			},
			want: Meta{
				Apply:                   "disabled",
				Version:                 "1.13.0",
				HashicorpVaultEnabled:   true,
				ShardingEnabled:         true,
				PMMEnabled:              true,
				SidecarsUsed:            true,
				BackupsEnabled:          true,
				ClusterSize:             2,
				PITREnabled:             true,
				HelmDeployCR:            true,
				PhysicalBackupScheduled: true,
				ClusterWideEnabled:      false,
				EncryptionEnabled:       true,
				TLSMode:                 string(api.TLSModePrefer),
				MongosSize:              2,
			},
			clusterWide: false,
			helmDeploy:  false,
			namespace:   "test-namespace",
		},
		"Disabled Backup with storage": {
			cr: api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name: "some-name",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Image: "percona/percona-server-mongodb:5.0.11-10",
					Replsets: []*api.ReplsetSpec{
						{
							Name:       "rs0",
							Size:       3,
							VolumeSpec: fakeVolumeSpec(t),
						},
					},
					Backup: api.BackupSpec{
						Enabled: false,
						Storages: map[string]api.BackupStorageSpec{
							"minio": {},
						},
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Size: 3,
				},
			},
			want: Meta{
				Apply:             "disabled",
				Version:           version.Version(),
				ClusterSize:       3,
				BackupsEnabled:    false,
				EncryptionEnabled: true,
				TLSMode:           string(api.TLSModePrefer),
			},
			namespace: "test-namespace",
		},
		"Cluster-wide with specified namespaces and operator helm deploy": {
			cr: api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name: "some-name",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Image: "percona/percona-server-mongodb:5.0.11-10",
					Replsets: []*api.ReplsetSpec{
						{
							Name:       "rs0",
							Size:       3,
							VolumeSpec: fakeVolumeSpec(t),
						},
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Size: 4,
				},
			},
			want: Meta{
				Apply:              "disabled",
				Version:            version.Version(),
				HelmDeployOperator: true,
				ClusterWideEnabled: true,
				ClusterSize:        4,
				EncryptionEnabled:  true,
				TLSMode:            string(api.TLSModePrefer),
			},
			clusterWide:     true,
			helmDeploy:      true,
			namespace:       "test-namespace",
			watchNamespaces: "test-namespace,another-namespace",
		},
		"Cluster-wide and operator helm deploy": {
			cr: api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name: "some-name",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Image: "percona/percona-server-mongodb:5.0.11-10",
					Replsets: []*api.ReplsetSpec{
						{
							Name:       "rs0",
							Size:       3,
							VolumeSpec: fakeVolumeSpec(t),
						},
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Size: 4,
				},
			},
			want: Meta{
				Apply:              "disabled",
				Version:            version.Version(),
				HelmDeployOperator: true,
				ClusterWideEnabled: true,
				ClusterSize:        4,
				EncryptionEnabled:  true,
				TLSMode:            string(api.TLSModePrefer),
			},
			clusterWide:     true,
			helmDeploy:      true,
			namespace:       "test-namespace",
			watchNamespaces: "",
		},
		"Encryption explicitly disabled by the user": {
			cr: api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name: "some-name",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Image: "percona/percona-server-mongodb:8.0.4-1",
					Replsets: []*api.ReplsetSpec{
						{
							Name:          "rs0",
							Size:          3,
							VolumeSpec:    fakeVolumeSpec(t),
							Configuration: "security:\n  enableEncryption: false\n",
						},
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Size: 3,
				},
			},
			want: Meta{
				Apply:             "disabled",
				Version:           version.Version(),
				ClusterSize:       3,
				EncryptionEnabled: false,
				TLSMode:           string(api.TLSModePrefer),
			},
			namespace: "test-namespace",
		},
		"Encryption explicitly enabled by the user": {
			cr: api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name: "some-name",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Image: "percona/percona-server-mongodb:8.0.4-1",
					Replsets: []*api.ReplsetSpec{
						{
							Name:          "rs0",
							Size:          3,
							VolumeSpec:    fakeVolumeSpec(t),
							Configuration: "security:\n  enableEncryption: true\n",
						},
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Size: 3,
				},
			},
			want: Meta{
				Apply:             "disabled",
				Version:           version.Version(),
				ClusterSize:       3,
				EncryptionEnabled: true,
				TLSMode:           string(api.TLSModePrefer),
			},
			namespace: "test-namespace",
		},
		"major upgrade requested overrides apply and mongo version": {
			cr: api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name: "some-name",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Image: "percona/percona-server-mongodb:8.0.4-1",
					UpgradeOptions: api.UpgradeOptions{
						Apply: "6.0-recommended",
					},
					Replsets: []*api.ReplsetSpec{
						{
							Name:       "rs0",
							Size:       3,
							VolumeSpec: fakeVolumeSpec(t),
						},
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Size:         3,
					MongoVersion: "5.0.11-10",
				},
			},
			fcv: "5.0",
			want: Meta{
				Apply:             "recommended",
				Version:           version.Version(),
				ClusterSize:       3,
				MongoVersion:      "6.0",
				EncryptionEnabled: true,
				TLSMode:           string(api.TLSModePrefer),
			},
			namespace: "test-namespace",
		},
		"major downgrade blocked by FCV": {
			cr: api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name: "some-name",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Image: "percona/percona-server-mongodb:8.0.4-1",
					UpgradeOptions: api.UpgradeOptions{
						Apply: "3.6",
					},
					Replsets: []*api.ReplsetSpec{
						{
							Name:       "rs0",
							Size:       3,
							VolumeSpec: fakeVolumeSpec(t),
						},
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Size:         3,
					MongoVersion: "4.0.3",
				},
			},
			fcv:        "4.0",
			wantErrMsg: "check if major update requested: can't upgrade to 3.6 with FCV set to 4.0",
			namespace:  "test-namespace",
		},
		"replset configuration has a non-bool enableEncryption": {
			cr: api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name: "some-name",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Image: "percona/percona-server-mongodb:8.0.4-1",
					Replsets: []*api.ReplsetSpec{
						{
							Name:       "rs0",
							Size:       3,
							VolumeSpec: fakeVolumeSpec(t),
							Configuration: `security:
  enableEncryption: "maybe"
`,
						},
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Size: 3,
				},
			},
			wantErrMsg: "enableEncryption value is not bool",
			namespace:  "test-namespace",
		},
		"CR with arbiter, non-voting, vector search, cluster sync and OCI/OSS storages": {
			cr: api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name: "some-name",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Image: "percona/percona-server-mongodb:8.0.4-1",
					Unsafe: api.UnsafeFlags{
						ReplsetSize: true,
					},
					TLS: &api.TLSSpec{
						Mode: api.TLSModeAllow,
					},
					Replsets: []*api.ReplsetSpec{
						{
							Name:       "rs0",
							Size:       3,
							VolumeSpec: fakeVolumeSpec(t),
							Arbiter: api.Arbiter{
								Enabled: true,
								Size:    1,
							},
							NonVoting: api.NonVotingSpec{
								Enabled:    true,
								Size:       1,
								VolumeSpec: fakeVolumeSpec(t),
							},
						},
					},
					Search: &api.SearchSpec{
						Enabled: true,
					},
					Backup: api.BackupSpec{
						Enabled: true,
						Storages: map[string]api.BackupStorageSpec{
							"oci-stg": {
								Type: api.BackupStorageOCI,
								Main: true,
							},
							"oss-stg": {
								Type: api.BackupStorageS3,
								S3: api.BackupStorageS3Spec{
									Bucket:      "test",
									EndpointURL: "https://s3.oss-eu-central-1.aliyuncs.com",
								},
							},
						},
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Size: 5,
				},
			},
			want: Meta{
				Apply:                "disabled",
				Version:              version.Version(),
				ClusterSize:          5,
				BackupsEnabled:       true,
				EncryptionEnabled:    true,
				VectorSearchEnabled:  true,
				ClusterSyncEnabled:   true,
				OCIBackupEnabled:     true,
				AlibabaBackupEnabled: true,
				ArbiterEnabled:       true,
				NonVotingEnabled:     true,
				TLSMode:              string(api.TLSModeAllow),
			},
			namespace: "test-namespace",
			extraObjects: []client.Object{
				&api.PerconaServerMongoDBClusterSync{
					Name: "some-name-sync",
					Spec: api.PerconaServerMongoDBClusterSyncSpec{
						ClusterName: "some-name",
					},
				},
			},
		},
		"CR with cluster sync targeting another cluster": {
			cr: api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name: "some-name",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Image: "percona/percona-server-mongodb:8.0.4-1",
					Replsets: []*api.ReplsetSpec{
						{
							Name:       "rs0",
							Size:       3,
							VolumeSpec: fakeVolumeSpec(t),
						},
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					Size: 3,
				},
			},
			want: Meta{
				Apply:             "disabled",
				Version:           version.Version(),
				ClusterSize:       3,
				EncryptionEnabled: true,
				TLSMode:           string(api.TLSModePrefer),
			},
			namespace: "test-namespace",
			extraObjects: []client.Object{
				&api.PerconaServerMongoDBClusterSync{
					Name: "other-name-sync",
					Spec: api.PerconaServerMongoDBClusterSyncSpec{
						ClusterName: "other-name",
					},
				},
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(k8s.WatchNamespaceEnvVar, tc.namespace)
			if tc.clusterWide {
				t.Setenv(k8s.WatchNamespaceEnvVar, tc.watchNamespaces)
			}
			operatorDepl := fakeOperatorDeployment(tc.helmDeploy)

			scheme := k8sruntime.NewScheme()
			require.NoError(t, clientgoscheme.AddToScheme(scheme), "add client-go scheme")
			require.NoError(t, apis.AddToScheme(scheme), "add apis scheme")

			cl := fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(&tc.cr, operatorDepl).
				WithObjects(tc.extraObjects...).
				Build()
			if tc.cr.Spec.CRVersion == "" {
				tc.cr.Spec.CRVersion = version.Version()
			}
			require.NoError(t, tc.cr.CheckNSetDefaults(ctx, version.PlatformKubernetes), "set CR defaults")

			vm, err := BuildMeta(ctx, cl, &tc.cr, operatorDepl, "", tc.fcv)
			if tc.wantErrMsg != "" {
				require.ErrorContains(t, err, tc.wantErrMsg)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, vm)
		})
	}
}
