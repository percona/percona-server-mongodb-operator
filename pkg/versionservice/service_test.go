package versionservice

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"

	pbVersion "github.com/Percona-Lab/percona-version-service/versionpb"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func startFakeVersionService(ctx context.Context, t *testing.T, addr string, port, gwport int) {
	t.Helper()

	s := grpc.NewServer()
	pbVersion.RegisterVersionServiceServer(s, new(fakeVS))

	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", addr, port))
	require.NoError(t, err, "listen interface")

	go func() {
		if err := s.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Error(errors.Wrap(err, "serve grpc server"))
		}
	}()
	t.Cleanup(s.GracefulStop)

	conn, err := grpc.NewClient(
		fmt.Sprintf("dns:///%s:%d", addr, port),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err, "dial server")
	t.Cleanup(func() {
		assert.NoError(t, conn.Close(), "close grpc client")
	})

	gwmux := runtime.NewServeMux()
	require.NoError(t, pbVersion.RegisterVersionServiceHandler(ctx, gwmux, conn), "register gateway")

	gwServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", addr, gwport),
		Handler: gwmux,
	}
	gwLis, err := net.Listen("tcp", gwServer.Addr)
	require.NoError(t, err, "listen gateway")

	go func() {
		if err := gwServer.Serve(gwLis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Error(errors.Wrap(err, "serve grpc-gateway"))
		}
	}()
	t.Cleanup(func() {
		assert.NoError(t, gwServer.Close(), "close grpc-gateway")
	})
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
		PmmVersion:              "3.1",
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

	if !proto.Equal(have, want) {
		return nil, errors.Errorf("have: %v; want: %v", have, want)
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
						"3.1": {
							ImagePath: "pmm3-image",
						},
					},
				},
			},
		},
	}, nil
}

func TestVersionService(t *testing.T) {
	ctx := t.Context()
	vs := Client{}
	tests := map[string]struct {
		cr         api.PerconaServerMongoDB
		vm         Meta
		want       Dep
		wantErrMsg string
	}{
		"UpgradeOptions.Apply: disabled": {
			cr: api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					UpgradeOptions: api.UpgradeOptions{
						Apply: api.UpgradeStrategyDisabled,
					},
				},
			},
			vm: Meta{
				Apply:   string(api.UpgradeStrategyDisabled),
				Version: version.Version(),
			},
			want: Dep{},
		},
		"UpgradeOptions.Apply: never": {
			cr: api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					UpgradeOptions: api.UpgradeOptions{
						Apply: api.UpgradeStrategyNever,
					},
				},
			},
			vm: Meta{
				Apply:   string(api.UpgradeStrategyNever),
				Version: version.Version(),
			},
			want: Dep{},
		},
		"Error on empty version service response": {
			cr:         api.PerconaServerMongoDB{},
			vm:         Meta{},
			want:       Dep{},
			wantErrMsg: "failed to version service apply",
		},
		"Request to version service": {
			cr: api.PerconaServerMongoDB{},
			vm: Meta{
				Apply:                   "",
				MongoVersion:            "database-version",
				KubeVersion:             "kube-version",
				Platform:                productName,
				PMMVersion:              "3.1",
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
			want: Dep{
				MongoImage:    "mongo-image",
				MongoVersion:  "mongo-version",
				BackupImage:   "backup-image",
				BackupVersion: "backup-version",
				PMMImage:      "pmm3-image",
				PMMVersion:    "3.1",
			},
		},
	}
	addr := "127.0.0.1"
	port := 10000
	gwPort := 11000
	startFakeVersionService(ctx, t, addr, port, gwPort)

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dv, err := vs.GetExactVersion(&tc.cr, fmt.Sprintf("http://%s:%d", addr, gwPort), tc.vm)
			if tc.wantErrMsg != "" {
				require.ErrorContains(t, err, tc.wantErrMsg)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, dv)
		})
	}
}
