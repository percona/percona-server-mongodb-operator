package pbm

import (
	"context"

	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/pkg/errors"
)

type MongoPBM struct {
	p   *pbm.PBM
	ctx context.Context
}

func NewMongoPBM(ctx context.Context, connectionURI string) (*MongoPBM, error) {
	cn, err := pbm.New(ctx, connectionURI, "e2e-tests-pbm")
	if err != nil {
		return nil, errors.Wrap(err, "connect")
	}

	return &MongoPBM{
		p:   cn,
		ctx: ctx,
	}, nil
}

func (m *MongoPBM) GetBackupMeta(bcpName string) (*pbm.BackupMeta, error) {
	return m.p.GetBackupMeta(bcpName)
}

func (m *MongoPBM) StoreResync() error {
	return m.p.ResyncBackupList()
}
