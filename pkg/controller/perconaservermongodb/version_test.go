package perconaservermongodb

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"testing"

	pbVersion "github.com/Percona-Lab/percona-version-service/versionpb"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/percona/percona-backup-mongodb/pbm/defs"

	"github.com/percona/percona-server-mongodb-operator/pkg/apis"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func Test_majorUpgradeRequested(t *testing.T) {
	type args struct {
		cr  *api.PerconaServerMongoDB
		fcv string
	}
	tests := []struct {
		name    string
		args    args
		want    UpgradeRequest
		wantErr bool
	}{
		{
			name: "TestWithEmptyMongoVersionInStatus",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2-recommended",
						},
					},
				},
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2",
				Apply:      "recommended",
			},
		},

		{
			name: "TestWithLowerMongoVersion",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2-recommended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0.3",
					},
				},
				fcv: "4.0",
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2",
				Apply:      "recommended",
			},
		},

		{
			name: "TestWithLowerMongoVersionAndOnlyVersionInApply",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0.3",
					},
				},
				fcv: "4.0",
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2",
			},
		},

		{
			name: "TestWithSameMongoVersion",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2-recommended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.2.3",
					},
				},
				fcv: "4.2",
			},
			want: UpgradeRequest{
				Ok: false,
			},
		},

		{
			name: "TestWithTooLowMongoVersion",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2-recommended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "3.6.3",
					},
				},
				fcv: "3.6",
			},
			wantErr: true,
		},

		{
			name: "TestWithInvalidVersionInApply",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.0.-4.0-recommended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0.3",
					},
				},
				fcv: "4.0",
			},
			wantErr: true,
		},

		{
			name: "TestWithRecommendedVersionInApplyField",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: api.UpgradeStrategyRecommended,
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "3.6.3",
					},
				},
				fcv: "3.6",
			},
			want: UpgradeRequest{
				Ok: false,
			},
		},

		{
			name: "TestWithLatestVersionInApplyField",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: api.UpgradeStrategyLatest,
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "3.6.3",
					},
				},
				fcv: "3.6",
			},
			want: UpgradeRequest{
				Ok: false,
			},
		},

		{
			name: "TestWithExactVersionInApplyField",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2.1-17",
						},
					},
				},
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2.1-17",
			},
		},

		{
			name: "TestWithExactVersionInApplyFieldAndNonEmptyVersionInMongoStatus",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2.1-17",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0.2-13",
					},
				},
				fcv: "4.0",
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2.1-17",
			},
		},

		{
			name: "TestInvalidDowngradeWithExactVersionInApply",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "3.6",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0.3",
					},
				},
				fcv: "4.0",
			},
			wantErr: true,
		},

		{
			name: "TestInvalidDowngradeWithPostfixVersionInApply",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "3.6-recommended",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.0.3",
					},
				},
				fcv: "4.0",
			},
			wantErr: true,
		},

		{
			name: "TestValidDowngradeWithExactVersionInApplyField",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2.13-14",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.4.1-17",
					},
				},
				fcv: "4.2",
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2.13-14",
			},
		},

		{
			name: "TestValidDowngradeWithPostfixVersionInApplyField",
			args: args{
				cr: &api.PerconaServerMongoDB{
					Spec: api.PerconaServerMongoDBSpec{
						UpgradeOptions: api.UpgradeOptions{
							Apply: "4.2-latest",
						},
					},
					Status: api.PerconaServerMongoDBStatus{
						MongoVersion: "4.4.1-17",
					},
				},
				fcv: "4.2",
			},
			want: UpgradeRequest{
				Ok:         true,
				NewVersion: "4.2",
				Apply:      "latest",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := majorUpgradeRequested(tt.args.cr, tt.args.fcv)
			if (err != nil) != tt.wantErr {
				t.Errorf("majorUpgradeRequested() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("majorUpgradeRequested() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVersionMeta(t *testing.T) {
	tests := []struct {
		name            string
		cr              api.PerconaServerMongoDB
		want            VersionMeta
		clusterWide     bool
		helmDeploy      bool
		namespace       string
		watchNamespaces string
	}{
		{
			name: "Minimal CR",
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
			want: VersionMeta{
				Apply:       "disabled",
				Version:     version.Version(),
				ClusterSize: 3,
			},
			namespace: "test-namespace",
		},
		{
			name: "Full CR with old Version deployed with Helm",
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
							MultiAZ: api.MultiAZ{
								Sidecars: []corev1.Container{
									{
										Name: "sidecar",
									},
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
			want: VersionMeta{
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
			},
			clusterWide: false,
			helmDeploy:  false,
			namespace:   "test-namespace",
		},
		{
			name: "Disabled Backup with storage",
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
			want: VersionMeta{
				Apply:          "disabled",
				Version:        version.Version(),
				ClusterSize:    3,
				BackupsEnabled: false,
			},
			namespace: "test-namespace",
		},
		{
			name: "Cluster-wide with specified namespaces and operator helm deploy",
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
			want: VersionMeta{
				Apply:              "disabled",
				Version:            version.Version(),
				HelmDeployOperator: true,
				ClusterWideEnabled: true,
				ClusterSize:        4,
			},
			clusterWide:     true,
			helmDeploy:      true,
			namespace:       "test-namespace",
			watchNamespaces: "test-namespace,another-namespace",
		},
		{
			name: "Cluster-wide and operator helm deploy",
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
			want: VersionMeta{
				Apply:              "disabled",
				Version:            version.Version(),
				HelmDeployOperator: true,
				ClusterWideEnabled: true,
				ClusterSize:        4,
			},
			clusterWide:     true,
			helmDeploy:      true,
			namespace:       "test-namespace",
			watchNamespaces: "",
		},
	}
	size := int32(1)
	operatorName := "percona-server-mongodb-operator"
	operatorDepl := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operatorName,
			Namespace: "",
			Labels:    make(map[string]string),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &size,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": "percona-server-mongodb-operator",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name": "percona-server-mongodb-operator",
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "percona-server-mongodb-operator",
					Containers: []corev1.Container{
						{
							Name: "percona-server-mongodb-operator",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(k8s.WatchNamespaceEnvVar, tt.namespace)
			if tt.clusterWide {
				t.Setenv(k8s.WatchNamespaceEnvVar, tt.watchNamespaces)
			}
			if tt.helmDeploy {
				operatorDepl.Labels["helm.sh/chart"] = operatorName
			} else {
				delete(operatorDepl.Labels, "helm.sh/chart")
			}

			scheme := k8sruntime.NewScheme()
			if err := clientgoscheme.AddToScheme(scheme); err != nil {
				t.Fatal(err, "failed to add client-go scheme")
			}
			if err := apis.AddToScheme(scheme); err != nil {
				t.Fatal(err, "failed to add apis scheme")
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&tt.cr, &operatorDepl).Build()
			sv := &version.ServerVersion{Platform: version.PlatformKubernetes}
			r := &ReconcilePerconaServerMongoDB{
				client:        cl,
				scheme:        scheme,
				serverVersion: sv,
			}

			if err := r.setCRVersion(context.TODO(), &tt.cr); err != nil {
				t.Fatal(err, "set CR version")
			}
			err := tt.cr.CheckNSetDefaults(context.TODO(), version.PlatformKubernetes)
			if err != nil {
				t.Fatal(err)
			}
			vm, err := r.getVersionMeta(context.TODO(), &tt.cr, &operatorDepl)
			if err != nil {
				t.Fatal(err)
			}
			if vm != tt.want {
				t.Fatalf("Have: %v; Want: %v", vm, tt.want)
			}
		})
	}
}

func startFakeVersionService(t *testing.T, addr string, port int, gwport int) error {
	s := grpc.NewServer()
	pbVersion.RegisterVersionServiceServer(s, new(fakeVS))

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", addr, port))
	if err != nil {
		return errors.Wrap(err, "failed to listen interface")
	}
	go func() {
		if err := s.Serve(lis); err != nil {
			t.Error(err, "failed to serve grpc server")
		}
	}()

	conn, err := grpc.DialContext(
		context.Background(),
		fmt.Sprintf("dns:///%s", fmt.Sprintf("%s:%d", addr, port)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return errors.Wrap(err, "failed to dial server")
	}

	gwmux := runtime.NewServeMux()
	err = pbVersion.RegisterVersionServiceHandler(context.Background(), gwmux, conn)
	if err != nil {
		return errors.Wrap(err, "failed to register gateway")
	}
	gwServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", addr, gwport),
		Handler: gwmux,
	}
	gwLis, err := net.Listen("tcp", gwServer.Addr)
	if err != nil {
		return errors.Wrap(err, "failed to listen gateway")
	}
	go func() {
		if err := gwServer.Serve(gwLis); err != nil {
			t.Error("failed to serve gRPC-Gateway", err)
		}
	}()

	return nil
}

type fakeVS struct{}

func (b *fakeVS) Product(ctx context.Context, req *pbVersion.ProductRequest) (*pbVersion.ProductResponse, error) {
	return &pbVersion.ProductResponse{}, nil
}

func (b *fakeVS) Operator(ctx context.Context, req *pbVersion.OperatorRequest) (*pbVersion.OperatorResponse, error) {
	return &pbVersion.OperatorResponse{}, nil
}

func (b *fakeVS) Apply(_ context.Context, req *pbVersion.ApplyRequest) (*pbVersion.VersionResponse, error) {
	switch req.Apply {
	case string(api.UpgradeStrategyNever), string(api.UpgradeStrategyDisabled):
		return &pbVersion.VersionResponse{}, nil
	}

	have := &pbVersion.ApplyRequest{
		BackupVersion:           req.GetBackupVersion(),
		ClusterWideEnabled:      req.GetClusterWideEnabled(),
		CustomResourceUid:       req.GetCustomResourceUid(),
		DatabaseVersion:         req.GetDatabaseVersion(),
		HashicorpVaultEnabled:   req.GetHashicorpVaultEnabled(),
		KubeVersion:             req.GetKubeVersion(),
		OperatorVersion:         req.GetOperatorVersion(),
		Platform:                req.GetPlatform(),
		PmmVersion:              req.GetPmmVersion(),
		ShardingEnabled:         req.GetShardingEnabled(),
		PmmEnabled:              req.GetPmmEnabled(),
		HelmDeployOperator:      req.GetHelmDeployOperator(),
		HelmDeployCr:            req.GetHelmDeployCr(),
		SidecarsUsed:            req.GetSidecarsUsed(),
		BackupsEnabled:          req.GetBackupsEnabled(),
		ClusterSize:             req.GetClusterSize(),
		PitrEnabled:             req.GetPitrEnabled(),
		PhysicalBackupScheduled: req.GetPhysicalBackupScheduled(),
	}
	want := &pbVersion.ApplyRequest{
		BackupVersion:           "backup-version",
		ClusterWideEnabled:      true,
		CustomResourceUid:       "custom-resource-uid",
		DatabaseVersion:         "database-version",
		HashicorpVaultEnabled:   true,
		KubeVersion:             "kube-version",
		OperatorVersion:         version.Version(),
		Platform:                productName,
		PmmVersion:              "pmm-version",
		ShardingEnabled:         true,
		PmmEnabled:              true,
		HelmDeployOperator:      true,
		HelmDeployCr:            true,
		SidecarsUsed:            true,
		BackupsEnabled:          true,
		ClusterSize:             3,
		PitrEnabled:             true,
		PhysicalBackupScheduled: true,
	}

	if !reflect.DeepEqual(have, want) {
		return nil, errors.Errorf("Have: %v; Want: %v", have, want)
	}

	return &pbVersion.VersionResponse{
		Versions: []*pbVersion.OperatorVersion{
			{
				Matrix: &pbVersion.VersionMatrix{
					Mongod: map[string]*pbVersion.Version{
						"mongo-version": {
							ImagePath: "mongo-image",
						},
					},
					Backup: map[string]*pbVersion.Version{
						"backup-version": {
							ImagePath: "backup-image",
						},
					},
					Pmm: map[string]*pbVersion.Version{
						"pmm-version": {
							ImagePath: "pmm-image",
						},
					},
				},
			},
		},
	}, nil
}

func TestVersionService(t *testing.T) {
	vs := VersionServiceClient{}
	tests := []struct {
		cr        api.PerconaServerMongoDB
		name      string
		vm        VersionMeta
		want      DepVersion
		shouldErr bool
	}{
		{
			name: "UpgradeOptions.Apply: disabled",
			cr: api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					UpgradeOptions: api.UpgradeOptions{
						Apply: api.UpgradeStrategyDisabled,
					},
				},
			},
			vm: VersionMeta{
				Apply:   string(api.UpgradeStrategyDisabled),
				Version: version.Version(),
			},
			want: DepVersion{},
		},
		{
			name: "UpgradeOptions.Apply: never",
			cr: api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					UpgradeOptions: api.UpgradeOptions{
						Apply: api.UpgradeStrategyNever,
					},
				},
			},
			vm: VersionMeta{
				Apply:   string(api.UpgradeStrategyNever),
				Version: version.Version(),
			},
			want: DepVersion{},
		},
		{
			name:      "Error on empty version service response",
			cr:        api.PerconaServerMongoDB{},
			vm:        VersionMeta{},
			want:      DepVersion{},
			shouldErr: true,
		},
		{
			name: "Request to version service",
			cr:   api.PerconaServerMongoDB{},
			vm: VersionMeta{
				Apply:                   "",
				MongoVersion:            "database-version",
				KubeVersion:             "kube-version",
				Platform:                productName,
				PMMVersion:              "pmm-version",
				BackupVersion:           "backup-version",
				CRUID:                   "custom-resource-uid",
				Version:                 version.Version(),
				ClusterWideEnabled:      true,
				HashicorpVaultEnabled:   true,
				ShardingEnabled:         true,
				PMMEnabled:              true,
				HelmDeployOperator:      true,
				HelmDeployCR:            true,
				SidecarsUsed:            true,
				BackupsEnabled:          true,
				ClusterSize:             3,
				PITREnabled:             true,
				PhysicalBackupScheduled: true,
			},
			want: DepVersion{
				MongoImage:    "mongo-image",
				MongoVersion:  "mongo-version",
				BackupImage:   "backup-image",
				BackupVersion: "backup-version",
				PMMImage:      "pmm-image",
				PMMVersion:    "pmm-version",
			},
		},
	}
	addr := "127.0.0.1"
	port := 10000
	gwPort := 11000
	if err := startFakeVersionService(t, addr, port, gwPort); err != nil {
		t.Fatal(err, "failed to start fake version service server")
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dv, err := vs.GetExactVersion(&tt.cr, fmt.Sprintf("http://%s:%d", addr, gwPort), tt.vm)
			if err != nil {
				if tt.shouldErr {
					return
				}
				t.Fatal(err)
			}
			if dv != tt.want {
				t.Fatal(errors.Errorf("Have: %v; Want: %v", dv, tt.want))
			}
		})
	}
}
