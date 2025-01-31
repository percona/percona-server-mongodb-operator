package restore

import (
	"context"
	"time"

	"github.com/mongodb/mongo-tools/common/db"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/restore/phys"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
)

func GetRestoreMetaByOPID(ctx context.Context, m connect.Client, opid string) (*RestoreMeta, error) {
	return getRestoreMeta(ctx, m, bson.D{{"opid", opid}})
}

func GetRestoreMeta(ctx context.Context, m connect.Client, name string) (*RestoreMeta, error) {
	return getRestoreMeta(ctx, m, bson.D{{"name", name}})
}

func getRestoreMeta(ctx context.Context, m connect.Client, clause bson.D) (*RestoreMeta, error) {
	res := m.RestoresCollection().FindOne(ctx, clause)
	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.ErrNotFound
		}
		return nil, errors.Wrap(err, "get")
	}
	r := &RestoreMeta{}
	err := res.Decode(r)
	return r, errors.Wrap(err, "decode")
}

func ChangeRestoreStateOPID(ctx context.Context, m connect.Client, opid string, s defs.Status, msg string) error {
	return changeRestoreState(ctx, m, bson.D{{"name", opid}}, s, msg)
}

func ChangeRestoreState(ctx context.Context, m connect.Client, name string, s defs.Status, msg string) error {
	return changeRestoreState(ctx, m, bson.D{{"name", name}}, s, msg)
}

func changeRestoreState(ctx context.Context, m connect.Client, clause bson.D, s defs.Status, msg string) error {
	ts := time.Now().UTC().Unix()
	_, err := m.RestoresCollection().UpdateOne(
		ctx,
		clause,
		bson.D{
			{"$set", bson.M{"status": s}},
			{"$set", bson.M{"last_transition_ts": ts}},
			{"$set", bson.M{"error": msg}},
			{"$push", bson.M{"conditions": Condition{Timestamp: ts, Status: s, Error: msg}}},
		},
	)

	return err
}

func ChangeRestoreRSState(
	ctx context.Context,
	m connect.Client,
	name,
	rsName string,
	s defs.Status,
	msg string,
) error {
	ts := time.Now().UTC().Unix()
	_, err := m.RestoresCollection().UpdateOne(
		ctx,
		bson.D{{"name", name}, {"replsets.name", rsName}},
		bson.D{
			{"$set", bson.M{"replsets.$.status": s}},
			{"$set", bson.M{"replsets.$.last_transition_ts": ts}},
			{"$set", bson.M{"replsets.$.error": msg}},
			{"$push", bson.M{"replsets.$.conditions": Condition{Timestamp: ts, Status: s, Error: msg}}},
		},
	)

	return err
}

func RestoreSetRSTxn(
	ctx context.Context,
	m connect.Client,
	name, rsName string,
	txn []phys.RestoreTxn,
) error {
	_, err := m.RestoresCollection().UpdateOne(
		ctx,
		bson.D{{"name", name}, {"replsets.name", rsName}},
		bson.D{{"$set", bson.M{"replsets.$.committed_txn": txn, "replsets.$.txn_set": true}}},
	)

	return err
}

func RestoreSetRSStat(
	ctx context.Context,
	m connect.Client,
	name, rsName string,
	stat phys.RestoreShardStat,
) error {
	_, err := m.RestoresCollection().UpdateOne(
		ctx,
		bson.D{{"name", name}, {"replsets.name", rsName}},
		bson.D{{"$set", bson.M{"replsets.$.stat": stat}}},
	)

	return err
}

func RestoreSetStat(ctx context.Context, m connect.Client, name string, stat phys.RestoreStat) error {
	_, err := m.RestoresCollection().UpdateOne(
		ctx,
		bson.D{{"name", name}},
		bson.D{{"$set", bson.M{"stat": stat}}},
	)

	return err
}

func RestoreSetRSPartTxn(ctx context.Context, m connect.Client, name, rsName string, txn []db.Oplog) error {
	_, err := m.RestoresCollection().UpdateOne(
		ctx,
		bson.D{{"name", name}, {"replsets.name", rsName}},
		bson.D{{"$set", bson.M{"replsets.$.partial_txn": txn}}},
	)

	return err
}

func SetCurrentOp(ctx context.Context, m connect.Client, name, rsName string, ts primitive.Timestamp) error {
	_, err := m.RestoresCollection().UpdateOne(
		ctx,
		bson.D{{"name", name}, {"replsets.name", rsName}},
		bson.D{{"$set", bson.M{"replsets.$.op": ts}}},
	)

	return err
}

func SetRestoreMeta(ctx context.Context, m connect.Client, meta *RestoreMeta) error {
	meta.LastTransitionTS = meta.StartTS
	meta.Conditions = append(meta.Conditions, &Condition{
		Timestamp: meta.StartTS,
		Status:    meta.Status,
	})

	_, err := m.RestoresCollection().InsertOne(ctx, meta)

	return err
}

func SetRestoreMetaIfNotExists(ctx context.Context, m connect.Client, meta *RestoreMeta) error {
	meta.LastTransitionTS = meta.StartTS
	meta.Conditions = append(meta.Conditions, &Condition{
		Timestamp: meta.StartTS,
		Status:    meta.Status,
	})

	_, err := m.RestoresCollection().UpdateOne(ctx,
		bson.D{{"name", meta.Name}},
		bson.D{{"$set", meta}},
		options.Update().SetUpsert(true))

	return err
}

// GetLastRestore returns last successfully finished restore
// and nil if there is no such restore yet.
func GetLastRestore(ctx context.Context, m connect.Client) (*RestoreMeta, error) {
	r := &RestoreMeta{}

	res := m.RestoresCollection().FindOne(
		ctx,
		bson.D{{"status", defs.StatusDone}},
		options.FindOne().SetSort(bson.D{{"start_ts", -1}}),
	)
	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.ErrNotFound
		}
		return nil, errors.Wrap(err, "get")
	}
	err := res.Decode(r)
	return r, errors.Wrap(err, "decode")
}

func AddRestoreRSMeta(ctx context.Context, m connect.Client, name string, rs RestoreReplset) error {
	rs.LastTransitionTS = rs.StartTS
	rs.Conditions = append(rs.Conditions, &Condition{
		Timestamp: rs.StartTS,
		Status:    rs.Status,
	})
	_, err := m.RestoresCollection().UpdateOne(
		ctx,
		bson.D{{"name", name}},
		bson.D{{"$addToSet", bson.M{"replsets": rs}}},
	)

	return err
}

func RestoreHB(ctx context.Context, m connect.Client, name string) error {
	ts, err := topo.GetClusterTime(ctx, m)
	if err != nil {
		return errors.Wrap(err, "read cluster time")
	}

	_, err = m.RestoresCollection().UpdateOne(
		ctx,
		bson.D{{"name", name}},
		bson.D{
			{"$set", bson.M{"hb": ts}},
		},
	)

	return errors.Wrap(err, "write into db")
}

func setRestoreBackup(ctx context.Context, m connect.Client, name, backupName string, nss []string) error {
	d := bson.M{"backup": backupName}
	if nss != nil {
		d["nss"] = nss
	}

	_, err := m.RestoresCollection().UpdateOne(
		ctx,
		bson.D{{"name", name}},
		bson.D{{"$set", d}},
	)

	return err
}

func SetOplogTimestamps(ctx context.Context, m connect.Client, name string, start, end int64) error {
	_, err := m.RestoresCollection().UpdateOne(
		ctx,
		bson.M{"name": name},
		bson.M{"$set": bson.M{"start_pitr": start, "pitr": end}},
	)

	return err
}

func RestoreList(ctx context.Context, m connect.Client, limit int64) ([]RestoreMeta, error) {
	opt := options.Find().SetLimit(limit).SetSort(bson.D{{"start_ts", -1}})
	cur, err := m.RestoresCollection().Find(ctx, bson.M{}, opt)
	if err != nil {
		return nil, errors.Wrap(err, "query")
	}

	restores := []RestoreMeta{}
	err = cur.All(ctx, &restores)
	return restores, err
}
