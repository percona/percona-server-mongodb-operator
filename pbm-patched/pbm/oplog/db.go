package oplog

import (
	"context"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

// mDB represents MongoDB access functionality.
type mDB struct {
	m *mongo.Client
}

func newMDB(m *mongo.Client) *mDB {
	return &mDB{m: m}
}

// getUUIDForNS ruturns UUID of existing collection.
// When ns doesn't exist, it returns zero value without an error.
// In case of error, it returns zero value for UUID in addition to error.
func (d *mDB) getUUIDForNS(ctx context.Context, ns string) (primitive.Binary, error) {
	var uuid primitive.Binary

	db, coll, _ := strings.Cut(ns, ".")
	cur, err := d.m.Database(db).ListCollections(ctx, bson.D{{"name", coll}})
	if err != nil {
		return uuid, errors.Wrap(err, "list collections")
	}
	defer cur.Close(ctx)

	for cur.Next(ctx) {
		if subtype, data, ok := cur.Current.Lookup("info", "uuid").BinaryOK(); ok {
			uuid = primitive.Binary{
				Subtype: subtype,
				Data:    data,
			}
			break
		}
	}

	return uuid, errors.Wrap(cur.Err(), "list collections cursor")
}

// ensureCollExists ensures that the collection exists before "creating" views or timeseries.
// See PBM-921 for details.
func (d *mDB) ensureCollExists(dbName string) error {
	err := d.m.Database(dbName).CreateCollection(context.TODO(), "system.views")
	if err != nil {
		// MongoDB 5.0 and 6.0 returns NamespaceExists error.
		// MongoDB 7.0 and 8.0 does not return error.
		// https://github.com/mongodb/mongo/blob/v6.0/src/mongo/base/error_codes.yml#L84
		const NamespaceExists = 48
		var cmdError mongo.CommandError
		if !errors.As(err, &cmdError) || cmdError.Code != NamespaceExists {
			return errors.Wrapf(err, "ensure %s.system.views collection", dbName)
		}
	}

	return nil
}

// applyOps is a wrapper for the applyOps database command, we pass in
// a session to avoid opening a new connection for a few inserts at a time.
func (d *mDB) applyOps(entries []interface{}) error {
	singleRes := d.m.Database("admin").RunCommand(context.TODO(), bson.D{{"applyOps", entries}})
	if err := singleRes.Err(); err != nil {
		return errors.Wrap(err, "applyOps")
	}
	res := bson.M{}
	err := singleRes.Decode(&res)
	if err != nil {
		return errors.Wrap(err, "decode singleRes")
	}
	if isFalsy(res["ok"]) {
		return errors.Errorf("applyOps command: %v", res["errmsg"])
	}

	return nil
}
