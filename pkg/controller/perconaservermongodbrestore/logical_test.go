package perconaservermongodbrestore

//go:generate ../../../bin/mockgen -source=../../../pkg/psmdb/backup/pbm.go -destination=mock_pbm.go -package=perconaservermongodbrestore

import (
	"errors"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/mongo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/oplog"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestGetRestoreCmd(t *testing.T) {
	t.Parallel()

	const (
		restoreName = "2026-05-18T12:00:00Z"
		backupName  = "my-backup"
	)

	ctx := t.Context()

	t.Run("populates all fields from spec", func(t *testing.T) {
		pbmc := NewMockPBM(gomock.NewController(t))

		rsMap := map[string]string{"rs0": "rs0-new", "rs1": "rs1-new"}
		cr := &psmdbv1.PerconaServerMongoDBRestore{
			Spec: psmdbv1.PerconaServerMongoDBRestoreSpec{
				RSMap: rsMap,
				Selective: &psmdbv1.SelectiveRestoreOpts{
					WithUsersAndRoles: true,
					Namespaces:        []string{"db1.coll1", "db2.coll2"},
					NamespaceFrom:     "db1.coll1",
					NamespaceTo:       "db1.coll1_renamed",
				},
			},
		}

		cmd, err := getRestoreCmd(ctx, pbmc, cr, restoreName, backupName)
		require.NoError(t, err)

		assert.Equal(t, ctrl.CmdRestore, cmd.Cmd)
		require.NotNil(t, cmd.Restore)
		assert.Equal(t, restoreName, cmd.Restore.Name)
		assert.Equal(t, backupName, cmd.Restore.BackupName)
		assert.Equal(t, rsMap, cmd.Restore.RSMap)
		assert.Equal(t, []string{"db1.coll1", "db2.coll2"}, cmd.Restore.Namespaces)
		assert.True(t, cmd.Restore.UsersAndRoles)
		assert.Equal(t, "db1.coll1", cmd.Restore.NamespaceFrom)
		assert.Equal(t, "db1.coll1_renamed", cmd.Restore.NamespaceTo)
		assert.Zero(t, cmd.Restore.OplogTS)
	})

	t.Run("nil Selective yields zero-value selective fields", func(t *testing.T) {
		ctx := t.Context()
		pbmc := NewMockPBM(gomock.NewController(t))

		cr := &psmdbv1.PerconaServerMongoDBRestore{
			Spec: psmdbv1.PerconaServerMongoDBRestoreSpec{
				RSMap:     map[string]string{"rs0": "rs0-new"},
				Selective: nil,
			},
		}

		cmd, err := getRestoreCmd(ctx, pbmc, cr, restoreName, backupName)
		require.NoError(t, err)

		assert.Equal(t, ctrl.CmdRestore, cmd.Cmd)
		require.NotNil(t, cmd.Restore)
		assert.Equal(t, restoreName, cmd.Restore.Name)
		assert.Equal(t, backupName, cmd.Restore.BackupName)
		assert.Equal(t, map[string]string{"rs0": "rs0-new"}, cmd.Restore.RSMap)
		assert.Nil(t, cmd.Restore.Namespaces)
		assert.False(t, cmd.Restore.UsersAndRoles)
		assert.Empty(t, cmd.Restore.NamespaceFrom)
		assert.Empty(t, cmd.Restore.NamespaceTo)
		assert.Zero(t, cmd.Restore.OplogTS)
	})

	t.Run("empty Selective yields zero-value selective fields", func(t *testing.T) {
		ctx := t.Context()
		pbmc := NewMockPBM(gomock.NewController(t))

		cr := &psmdbv1.PerconaServerMongoDBRestore{
			Spec: psmdbv1.PerconaServerMongoDBRestoreSpec{
				Selective: &psmdbv1.SelectiveRestoreOpts{},
			},
		}

		cmd, err := getRestoreCmd(ctx, pbmc, cr, restoreName, backupName)
		require.NoError(t, err)

		require.NotNil(t, cmd.Restore)
		assert.Nil(t, cmd.Restore.Namespaces)
		assert.False(t, cmd.Restore.UsersAndRoles)
		assert.Empty(t, cmd.Restore.NamespaceFrom)
		assert.Empty(t, cmd.Restore.NamespaceTo)
	})

	t.Run("nil RSMap is propagated", func(t *testing.T) {
		ctx := t.Context()
		pbmc := NewMockPBM(gomock.NewController(t))

		cr := &psmdbv1.PerconaServerMongoDBRestore{
			Spec: psmdbv1.PerconaServerMongoDBRestoreSpec{},
		}

		cmd, err := getRestoreCmd(ctx, pbmc, cr, restoreName, backupName)
		require.NoError(t, err)

		require.NotNil(t, cmd.Restore)
		assert.Nil(t, cmd.Restore.RSMap)
	})

	t.Run("namespace remap without selective namespaces", func(t *testing.T) {
		ctx := t.Context()
		pbmc := NewMockPBM(gomock.NewController(t))

		cr := &psmdbv1.PerconaServerMongoDBRestore{
			Spec: psmdbv1.PerconaServerMongoDBRestoreSpec{
				Selective: &psmdbv1.SelectiveRestoreOpts{
					NamespaceFrom: "olddb.coll",
					NamespaceTo:   "newdb.coll",
				},
			},
		}

		cmd, err := getRestoreCmd(ctx, pbmc, cr, restoreName, backupName)
		require.NoError(t, err)

		require.NotNil(t, cmd.Restore)
		assert.Equal(t, "olddb.coll", cmd.Restore.NamespaceFrom)
		assert.Equal(t, "newdb.coll", cmd.Restore.NamespaceTo)
		assert.Nil(t, cmd.Restore.Namespaces)
		assert.False(t, cmd.Restore.UsersAndRoles)
	})

	t.Run("users-and-roles only", func(t *testing.T) {
		ctx := t.Context()
		pbmc := NewMockPBM(gomock.NewController(t))

		cr := &psmdbv1.PerconaServerMongoDBRestore{
			Spec: psmdbv1.PerconaServerMongoDBRestoreSpec{
				Selective: &psmdbv1.SelectiveRestoreOpts{
					WithUsersAndRoles: true,
				},
			},
		}

		cmd, err := getRestoreCmd(ctx, pbmc, cr, restoreName, backupName)
		require.NoError(t, err)

		require.NotNil(t, cmd.Restore)
		assert.True(t, cmd.Restore.UsersAndRoles)
		assert.Nil(t, cmd.Restore.Namespaces)
		assert.Empty(t, cmd.Restore.NamespaceFrom)
		assert.Empty(t, cmd.Restore.NamespaceTo)
	})

	t.Run("PITR by date sets OplogTS from spec date", func(t *testing.T) {
		ctx := t.Context()
		pbmc := NewMockPBM(gomock.NewController(t))

		date := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
		ts := date.Unix()
		rsMap := map[string]string{"rs0": "rs0-new"}

		pbmc.EXPECT().
			GetPITRChunkContains(ctx, ts, rsMap).
			Return(&oplog.OplogChunk{}, nil)

		cr := &psmdbv1.PerconaServerMongoDBRestore{
			Spec: psmdbv1.PerconaServerMongoDBRestoreSpec{
				RSMap: rsMap,
				PITR: &psmdbv1.PITRestoreSpec{
					Type: psmdbv1.PITRestoreTypeDate,
					Date: &psmdbv1.PITRestoreDate{Time: metav1.NewTime(date)},
				},
			},
		}

		cmd, err := getRestoreCmd(ctx, pbmc, cr, restoreName, backupName)
		require.NoError(t, err)
		require.NotNil(t, cmd.Restore)
		assert.Equal(t, uint32(ts), cmd.Restore.OplogTS.T)
	})

	t.Run("PITR by date returns error from PBM", func(t *testing.T) {
		ctx := t.Context()
		pbmc := NewMockPBM(gomock.NewController(t))

		date := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
		boom := errors.New("boom")

		pbmc.EXPECT().
			GetPITRChunkContains(ctx, date.Unix(), gomock.Any()).
			Return(nil, boom)

		cr := &psmdbv1.PerconaServerMongoDBRestore{
			Spec: psmdbv1.PerconaServerMongoDBRestoreSpec{
				PITR: &psmdbv1.PITRestoreSpec{
					Type: psmdbv1.PITRestoreTypeDate,
					Date: &psmdbv1.PITRestoreDate{Time: metav1.NewTime(date)},
				},
			},
		}

		cmd, err := getRestoreCmd(ctx, pbmc, cr, restoreName, backupName)
		assert.ErrorIs(t, err, boom)
		assert.Equal(t, ctrl.Cmd{}, cmd)
	})

	t.Run("PITR latest sets OplogTS from timeline end", func(t *testing.T) {
		ctx := t.Context()
		pbmc := NewMockPBM(gomock.NewController(t))

		rsMap := map[string]string{"rs0": "rs0-new"}
		const tlEnd uint32 = 1763_424_000

		pbmc.EXPECT().
			GetLatestTimelinePITR(ctx, rsMap).
			Return(oplog.Timeline{End: tlEnd}, nil)

		cr := &psmdbv1.PerconaServerMongoDBRestore{
			Spec: psmdbv1.PerconaServerMongoDBRestoreSpec{
				RSMap: rsMap,
				PITR: &psmdbv1.PITRestoreSpec{
					Type: psmdbv1.PITRestoreTypeLatest,
				},
			},
		}

		cmd, err := getRestoreCmd(ctx, pbmc, cr, restoreName, backupName)
		require.NoError(t, err)
		require.NotNil(t, cmd.Restore)
		assert.Equal(t, tlEnd, cmd.Restore.OplogTS.T)
	})

	t.Run("PITR latest returns error from PBM", func(t *testing.T) {
		ctx := t.Context()
		pbmc := NewMockPBM(gomock.NewController(t))

		boom := errors.New("boom")
		pbmc.EXPECT().
			GetLatestTimelinePITR(ctx, gomock.Any()).
			Return(oplog.Timeline{}, boom)

		cr := &psmdbv1.PerconaServerMongoDBRestore{
			Spec: psmdbv1.PerconaServerMongoDBRestoreSpec{
				PITR: &psmdbv1.PITRestoreSpec{
					Type: psmdbv1.PITRestoreTypeLatest,
				},
			},
		}

		cmd, err := getRestoreCmd(ctx, pbmc, cr, restoreName, backupName)
		assert.ErrorIs(t, err, boom)
		assert.Equal(t, ctrl.Cmd{}, cmd)
	})
}

func TestRestorePBMConfig(t *testing.T) {
	t.Parallel()

	s3Storage := func(prefix string) psmdbv1.BackupStorageSpec {
		return psmdbv1.BackupStorageSpec{
			Type: psmdbv1.BackupStorageS3,
			S3: psmdbv1.BackupStorageS3Spec{
				Bucket: "operator-testing",
				Prefix: prefix,
				Region: "us-east-1",
			},
		}
	}
	newCluster := func(crVersion string, storages map[string]psmdbv1.BackupStorageSpec) *psmdbv1.PerconaServerMongoDB {
		return &psmdbv1.PerconaServerMongoDB{
			Name: "cluster2", Namespace: "pitr",
			Spec: psmdbv1.PerconaServerMongoDBSpec{
				CRVersion: crVersion,
				Backup: psmdbv1.BackupSpec{
					Enabled:  true,
					Storages: storages,
					PITR:     psmdbv1.PITRSpec{Enabled: true},
				},
			},
			Status: psmdbv1.PerconaServerMongoDBStatus{BackupVersion: "2.17.0"},
		}
	}
	pbmConfigWith := func(t *testing.T, cluster *psmdbv1.PerconaServerMongoDB, stg psmdbv1.BackupStorageSpec) *config.Config {
		cfg, err := backup.GetPBMConfig(t.Context(), fakeReconciler().client, cluster, stg)
		require.NoError(t, err)
		return &cfg
	}

	t.Run("main storage was changed by the restore", func(t *testing.T) {
		t.Parallel()

		cluster := newCluster(version.Version(), map[string]psmdbv1.BackupStorageSpec{"local-s3": s3Storage("pitr-prefix-2")})
		r := fakeReconciler(cluster)
		pbmc := NewMockPBM(gomock.NewController(t))

		gomock.InOrder(
			pbmc.EXPECT().GetConfig(gomock.Any()).Return(pbmConfigWith(t, cluster, s3Storage("pitr-prefix-1")), nil),
			pbmc.EXPECT().GetNSetConfig(gomock.Any(), r.client, cluster).Return(nil),
			pbmc.EXPECT().ResyncMainStorageAndWait(gomock.Any()).Return(nil),
		)

		require.NoError(t, r.restorePBMConfig(t.Context(), pbmc, cluster))
	})

	t.Run("main storage is unchanged", func(t *testing.T) {
		t.Parallel()

		cluster := newCluster(version.Version(), map[string]psmdbv1.BackupStorageSpec{"local-s3": s3Storage("pitr-prefix-2")})
		r := fakeReconciler(cluster)
		pbmc := NewMockPBM(gomock.NewController(t))

		gomock.InOrder(
			pbmc.EXPECT().GetConfig(gomock.Any()).Return(pbmConfigWith(t, cluster, s3Storage("pitr-prefix-2")), nil),
			pbmc.EXPECT().SetConfigVar(gomock.Any(), "pitr.enabled", "true").Return(nil),
		)

		require.NoError(t, r.restorePBMConfig(t.Context(), pbmc, cluster))
	})

	t.Run("no PBM config yet", func(t *testing.T) {
		t.Parallel()

		cluster := newCluster(version.Version(), map[string]psmdbv1.BackupStorageSpec{"local-s3": s3Storage("pitr-prefix-2")})
		r := fakeReconciler(cluster)
		pbmc := NewMockPBM(gomock.NewController(t))

		gomock.InOrder(
			pbmc.EXPECT().GetConfig(gomock.Any()).Return(nil, mongo.ErrNoDocuments),
			pbmc.EXPECT().GetNSetConfig(gomock.Any(), r.client, cluster).Return(nil),
			pbmc.EXPECT().ResyncMainStorageAndWait(gomock.Any()).Return(nil),
		)

		require.NoError(t, r.restorePBMConfig(t.Context(), pbmc, cluster))
	})

	errorTests := map[string]struct {
		expect  func(pbmc *MockPBM, r *ReconcilePerconaServerMongoDBRestore, cluster *psmdbv1.PerconaServerMongoDB, staleCfg *config.Config)
		wantErr string
	}{
		"get current config fails": {
			expect: func(pbmc *MockPBM, _ *ReconcilePerconaServerMongoDBRestore, _ *psmdbv1.PerconaServerMongoDB, _ *config.Config) {
				pbmc.EXPECT().GetConfig(gomock.Any()).Return(nil, assert.AnError)
			},
			wantErr: "get current PBM config",
		},
		"set config fails": {
			expect: func(pbmc *MockPBM, r *ReconcilePerconaServerMongoDBRestore, cluster *psmdbv1.PerconaServerMongoDB, staleCfg *config.Config) {
				gomock.InOrder(
					pbmc.EXPECT().GetConfig(gomock.Any()).Return(staleCfg, nil),
					pbmc.EXPECT().GetNSetConfig(gomock.Any(), r.client, cluster).Return(assert.AnError),
				)
			},
			wantErr: "set PBM config",
		},
		"resync fails": {
			expect: func(pbmc *MockPBM, r *ReconcilePerconaServerMongoDBRestore, cluster *psmdbv1.PerconaServerMongoDB, staleCfg *config.Config) {
				gomock.InOrder(
					pbmc.EXPECT().GetConfig(gomock.Any()).Return(staleCfg, nil),
					pbmc.EXPECT().GetNSetConfig(gomock.Any(), r.client, cluster).Return(nil),
					pbmc.EXPECT().ResyncMainStorageAndWait(gomock.Any()).Return(assert.AnError),
				)
			},
			wantErr: "resync main storage",
		},
	}
	for name, tt := range errorTests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cluster := newCluster(version.Version(), map[string]psmdbv1.BackupStorageSpec{"local-s3": s3Storage("pitr-prefix-2")})
			r := fakeReconciler(cluster)
			pbmc := NewMockPBM(gomock.NewController(t))
			tt.expect(pbmc, r, cluster, pbmConfigWith(t, cluster, s3Storage("pitr-prefix-1")))

			err := r.restorePBMConfig(t.Context(), pbmc, cluster)
			require.ErrorIs(t, err, assert.AnError)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}

	t.Run("no main storage", func(t *testing.T) {
		t.Parallel()

		cluster := newCluster(version.Version(), nil)
		r := fakeReconciler(cluster)
		pbmc := NewMockPBM(gomock.NewController(t))

		require.NoError(t, r.restorePBMConfig(t.Context(), pbmc, cluster))
	})

	t.Run("crVersion before 1.24.0", func(t *testing.T) {
		t.Parallel()

		cluster := newCluster("1.23.0", map[string]psmdbv1.BackupStorageSpec{"local-s3": s3Storage("pitr-prefix-2")})
		r := fakeReconciler(cluster)
		pbmc := NewMockPBM(gomock.NewController(t))

		pbmc.EXPECT().SetConfigVar(gomock.Any(), "pitr.enabled", "true").Return(nil)

		require.NoError(t, r.restorePBMConfig(t.Context(), pbmc, cluster))
	})
}
