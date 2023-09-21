package fake

import (
	"context"

	"github.com/percona/percona-backup-mongodb/pbm"
	pbmLog "github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

type fakePBM struct {
}

func NewPBM(_ context.Context, _ client.Client, _ *api.PerconaServerMongoDB) (backup.PBM, error) {
	return new(fakePBM), nil
}
func (p *fakePBM) Conn() *mongo.Client {
	return nil
}
func (p *fakePBM) GetPITRChunkContains(ctx context.Context, unixTS int64) (*pbm.OplogChunk, error) {
	return nil, nil
}
func (p *fakePBM) GetLatestTimelinePITR() (pbm.Timeline, error) {
	return pbm.Timeline{}, nil
}
func (p *fakePBM) PITRGetChunksSlice(rs string, from, to primitive.Timestamp) ([]pbm.OplogChunk, error) {
	return nil, nil
}
func (p *fakePBM) Logger() *pbmLog.Logger {
	return nil
}
func (p *fakePBM) GetStorage(l *pbmLog.Event) (storage.Storage, error) {
	return nil, nil
}
func (p *fakePBM) ResyncStorage(l *pbmLog.Event) error {
	return nil
}
func (p *fakePBM) SendCmd(cmd pbm.Cmd) error {
	return nil
}
func (p *fakePBM) Close(ctx context.Context) error {
	return nil
}
func (p *fakePBM) HasLocks(predicates ...backup.LockHeaderPredicate) (bool, error) {
	return false, nil
}
func (p *fakePBM) GetRestoreMeta(name string) (*pbm.RestoreMeta, error) {
	return nil, nil
}
func (p *fakePBM) GetBackupMeta(name string) (*pbm.BackupMeta, error) {
	return nil, nil
}
func (p *fakePBM) DeleteBackup(name string, l *pbmLog.Event) error {
	return nil
}
func (p *fakePBM) SetConfig(ctx context.Context, k8sclient client.Client, cluster *api.PerconaServerMongoDB, stg api.BackupStorageSpec) error {
	return nil
}
func (p *fakePBM) SetConfigVar(key, val string) error {
	return nil
}
func (p *fakePBM) GetConfigVar(key string) (any, error) {
	return nil, nil
}
func (p *fakePBM) DeleteConfigVar(key string) error {
	return nil
}

func (p *fakePBM) Node() (string, error) {
	return "", nil
}
