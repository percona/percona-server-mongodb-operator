package fake

import (
	"context"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	pbmLog "github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/oplog"
	"github.com/percona/percona-backup-mongodb/pbm/restore"
	"github.com/percona/percona-backup-mongodb/pbm/storage"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

type fakePBM struct{}

func NewPBM(_ context.Context, _ client.Client, _ *api.PerconaServerMongoDB) (backup.PBM, error) {
	return new(fakePBM), nil
}

func (p *fakePBM) Conn() *mongo.Client {
	return nil
}

func (p *fakePBM) GetPITRChunkContains(ctx context.Context, unixTS int64) (*oplog.OplogChunk, error) {
	return nil, nil
}

func (p *fakePBM) GetLatestTimelinePITR(ctx context.Context) (oplog.Timeline, error) {
	return oplog.Timeline{}, nil
}

func (p *fakePBM) PITRGetChunksSlice(ctx context.Context, rs string, from, to primitive.Timestamp) ([]oplog.OplogChunk, error) {
	return nil, nil
}

func (b *fakePBM) PITRChunksCollection() *mongo.Collection {
	return nil
}

func (p *fakePBM) Logger() pbmLog.Logger {
	return nil
}

func (p *fakePBM) GetStorage(ctx context.Context, e pbmLog.LogEvent) (storage.Storage, error) {
	return nil, nil
}

func (p *fakePBM) ResyncMainStorage(ctx context.Context) error {
	return nil
}

func (p *fakePBM) ResyncMainStorageAndWait(ctx context.Context) error {
	return nil
}

func (p *fakePBM) ResyncProfile(ctx context.Context, name string) error {
	return nil
}

func (p *fakePBM) ResyncProfileAndWait(ctx context.Context, name string) error {
	return nil
}

func (p *fakePBM) SendCmd(ctx context.Context, cmd ctrl.Cmd) error {
	return nil
}

func (p *fakePBM) Close(ctx context.Context) error {
	return nil
}

func (p *fakePBM) HasLocks(ctx context.Context, predicates ...backup.LockHeaderPredicate) (bool, error) {
	return false, nil
}

func (p *fakePBM) GetRestoreMeta(ctx context.Context, name string) (*restore.RestoreMeta, error) {
	return nil, nil
}

func (p *fakePBM) GetBackupMeta(ctx context.Context, bcpName string) (*backup.BackupMeta, error) {
	return nil, nil
}

func (p *fakePBM) DeleteBackup(ctx context.Context, name string) error {
	return nil
}

func (p *fakePBM) GetProfile(ctx context.Context, name string) (*config.Config, error) {
	return nil, nil
}

func (p *fakePBM) RemoveProfile(ctx context.Context, name string) error {
	return nil
}

func (p *fakePBM) AddProfile(ctx context.Context, k8sclient client.Client, cluster *api.PerconaServerMongoDB, name string, stg api.BackupStorageSpec) error {
	return nil
}

func (p *fakePBM) GetNSetConfig(ctx context.Context, k8sclient client.Client, cluster *api.PerconaServerMongoDB) error {
	return nil
}

func (p *fakePBM) SetConfigVar(ctx context.Context, key, val string) error {
	return nil
}

func (p *fakePBM) SetConfig(ctx context.Context, cfg *config.Config) error {
	return nil
}

func (p *fakePBM) GetConfig(ctx context.Context) (*config.Config, error) {
	return nil, nil
}

func (p *fakePBM) GetConfigVar(ctx context.Context, key string) (any, error) {
	return nil, nil
}

func (p *fakePBM) DeleteConfigVar(ctx context.Context, key string) error {
	return nil
}

func (p *fakePBM) Node(ctx context.Context) (string, error) {
	return "", nil
}

func (p *fakePBM) ValidateBackup(ctx context.Context, cfg *config.Config, bcp *psmdbv1.PerconaServerMongoDBBackup) error {
	return nil
}

func (p *fakePBM) DeletePITRChunks(ctx context.Context, until primitive.Timestamp) error {
	return nil
}
