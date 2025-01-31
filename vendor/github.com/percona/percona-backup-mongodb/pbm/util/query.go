package util

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

func DropTMPcoll(ctx context.Context, cn *mongo.Client) error {
	err := cn.Database(defs.DB).Collection(defs.TmpRolesCollection).Drop(ctx)
	if err != nil {
		return errors.Wrapf(err, "drop collection %s", defs.TmpRolesCollection)
	}

	err = cn.Database(defs.DB).Collection(defs.TmpUsersCollection).Drop(ctx)
	if err != nil {
		return errors.Wrapf(err, "drop collection %s", defs.TmpUsersCollection)
	}

	return nil
}
