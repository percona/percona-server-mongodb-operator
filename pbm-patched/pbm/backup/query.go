package backup

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
)

type Manager interface {
	GetAllBackups(ctx context.Context) ([]BackupMeta, error)
	GetBackupByName(ctx context.Context, name string) (*BackupMeta, error)
	GetBackupByOpID(ctx context.Context, opid string) (*BackupMeta, error)
}

type dbMangerImpl struct {
	conn connect.Client
}

func NewDBManager(conn connect.Client) *dbMangerImpl {
	return &dbMangerImpl{conn: conn}
}

func (m *dbMangerImpl) GetAllBackups(ctx context.Context) ([]BackupMeta, error) {
	cur, err := m.conn.BcpCollection().Find(ctx, bson.D{})
	if err != nil {
		return nil, err
	}

	rv := []BackupMeta{}
	err = cur.All(ctx, &rv)
	return rv, err
}

func (m *dbMangerImpl) GetBackupByName(ctx context.Context, name string) (*BackupMeta, error) {
	return m.query(ctx, bson.D{{"name", name}})
}

func (m *dbMangerImpl) GetBackupByOpID(ctx context.Context, id string) (*BackupMeta, error) {
	return m.query(ctx, bson.D{{"opid", id}})
}

func (m *dbMangerImpl) query(ctx context.Context, clause bson.D) (*BackupMeta, error) {
	res := m.conn.BcpCollection().FindOne(ctx, clause)
	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.ErrNotFound
		}
		return nil, errors.Wrap(err, "get")
	}

	b := &BackupMeta{}
	err := res.Decode(b)
	return b, errors.Wrap(err, "decode")
}

func GetBackupByOPID(ctx context.Context, conn connect.Client, opid string) (*BackupMeta, error) {
	return getBackupMeta(ctx, conn, bson.D{{"opid", opid}})
}

func getBackupMeta(ctx context.Context, conn connect.Client, clause bson.D) (*BackupMeta, error) {
	res := conn.BcpCollection().FindOne(ctx, clause)
	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.ErrNotFound
		}
		return nil, errors.Wrap(err, "get")
	}

	b := &BackupMeta{}
	err := res.Decode(b)
	return b, errors.Wrap(err, "decode")
}

func ChangeBackupStateOPID(conn connect.Client, opid string, s defs.Status, msg string) error {
	return changeBackupState(context.TODO(),
		conn, bson.D{{"opid", opid}}, time.Now().UTC().Unix(), s, msg)
}

func ChangeBackupState(conn connect.Client, bcpName string, s defs.Status, msg string) error {
	return changeBackupState(context.TODO(),
		conn, bson.D{{"name", bcpName}}, time.Now().UTC().Unix(), s, msg)
}

func ChangeBackupStateWithUnixTime(
	ctx context.Context,
	conn connect.Client,
	bcpName string,
	s defs.Status,
	unix int64,
	msg string,
) error {
	return changeBackupState(ctx, conn, bson.D{{"name", bcpName}}, time.Now().UTC().Unix(), s, msg)
}

func changeBackupState(
	ctx context.Context,
	conn connect.Client,
	clause bson.D,
	ts int64,
	s defs.Status,
	msg string,
) error {
	_, err := conn.BcpCollection().UpdateOne(
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

func saveBackupMeta(ctx context.Context, conn connect.Client, meta *BackupMeta) error {
	meta.LastTransitionTS = meta.StartTS
	meta.Conditions = append(meta.Conditions, Condition{
		Timestamp: meta.StartTS,
		Status:    meta.Status,
	})

	_, err := conn.BcpCollection().InsertOne(ctx, meta)

	return err
}

func BackupHB(ctx context.Context, conn connect.Client, bcpName string) error {
	ts, err := topo.GetClusterTime(ctx, conn)
	if err != nil {
		return errors.Wrap(err, "read cluster time")
	}

	_, err = conn.BcpCollection().UpdateOne(
		ctx,
		bson.D{{"name", bcpName}},
		bson.D{
			{"$set", bson.M{"hb": ts}},
		},
	)

	return errors.Wrap(err, "write into db")
}

func SetSrcBackup(ctx context.Context, conn connect.Client, bcpName, srcName string) error {
	_, err := conn.BcpCollection().UpdateOne(
		ctx,
		bson.D{{"name", bcpName}},
		bson.D{
			{"$set", bson.M{"src_backup": srcName}},
		},
	)

	return err
}

func SetFirstWrite(ctx context.Context, conn connect.Client, bcpName string, first primitive.Timestamp) error {
	_, err := conn.BcpCollection().UpdateOne(
		ctx,
		bson.D{{"name", bcpName}},
		bson.D{
			{"$set", bson.M{"first_write_ts": first}},
		},
	)

	return err
}

func SetLastWrite(ctx context.Context, conn connect.Client, bcpName string, last primitive.Timestamp) error {
	_, err := conn.BcpCollection().UpdateOne(
		ctx,
		bson.D{{"name", bcpName}},
		bson.D{
			{"$set", bson.M{"last_write_ts": last}},
		},
	)

	return err
}

func AddRSMeta(ctx context.Context, conn connect.Client, bcpName string, rs BackupReplset) error {
	rs.LastTransitionTS = rs.StartTS
	rs.Conditions = append(rs.Conditions, Condition{
		Timestamp: rs.StartTS,
		Status:    rs.Status,
	})
	_, err := conn.BcpCollection().UpdateOne(
		ctx,
		bson.D{{"name", bcpName}},
		bson.D{{"$addToSet", bson.M{"replsets": rs}}},
	)

	return err
}

func ChangeRSState(conn connect.Client, bcpName, rsName string, s defs.Status, msg string) error {
	ts := time.Now().UTC().Unix()
	_, err := conn.BcpCollection().UpdateOne(
		context.Background(),
		bson.D{{"name", bcpName}, {"replsets.name", rsName}},
		bson.D{
			{"$set", bson.M{"replsets.$.status": s}},
			{"$set", bson.M{"replsets.$.last_transition_ts": ts}},
			{"$set", bson.M{"replsets.$.error": msg}},
			{"$push", bson.M{"replsets.$.conditions": Condition{Timestamp: ts, Status: s, Error: msg}}},
		},
	)

	return err
}

// IncBackupSize increments total backup size.
func IncBackupSize(
	ctx context.Context,
	conn connect.Client,
	bcpName string,
	size int64,
	sizeUncompressed *int64,
) error {
	update := bson.D{
		{"$inc", bson.M{"size": size}},
	}
	if sizeUncompressed != nil {
		update = append(
			update,
			bson.E{"$inc", bson.M{"size_uncompressed": sizeUncompressed}},
		)
	}

	_, err := conn.BcpCollection().UpdateOne(ctx,
		bson.D{{"name", bcpName}},
		update,
	)

	return err
}

// SetBackupSizeForRS sets size of backup for specified RS.
func SetBackupSizeForRS(
	ctx context.Context,
	conn connect.Client,
	bcpName,
	rsName string,
	size int64,
	sizeUncompressed int64,
) error {
	_, err := conn.BcpCollection().UpdateOne(
		ctx,
		bson.D{{"name", bcpName}, {"replsets.name", rsName}},
		bson.D{
			{"$set", bson.M{"replsets.$.size": size}},
			{"$set", bson.M{"replsets.$.size_uncompressed": sizeUncompressed}},
		},
	)
	return err
}

func SetRSLastWrite(conn connect.Client, bcpName, rsName string, ts primitive.Timestamp) error {
	_, err := conn.BcpCollection().UpdateOne(
		context.Background(),
		bson.D{{"name", bcpName}, {"replsets.name", rsName}},
		bson.D{
			{"$set", bson.M{"replsets.$.last_write_ts": ts}},
		},
	)

	return err
}

func LastIncrementalBackup(ctx context.Context, conn connect.Client) (*BackupMeta, error) {
	return getRecentBackup(ctx, conn, nil, nil, -1, bson.D{{"type", string(defs.IncrementalBackup)}})
}

// GetLastBackup returns last successfully finished backup (non-selective and non-external)
// or nil if there is no such backup yet. If ts isn't nil it will
// search for the most recent backup that finished before specified timestamp
func GetLastBackup(ctx context.Context, conn connect.Client, before *primitive.Timestamp) (*BackupMeta, error) {
	return getRecentBackup(ctx, conn, nil, before, -1, bson.D{
		{"nss", nil},
		{"type", bson.M{"$ne": defs.ExternalBackup}},
		{"store.profile", nil},
	})
}

func GetFirstBackup(ctx context.Context, conn connect.Client, after *primitive.Timestamp) (*BackupMeta, error) {
	return getRecentBackup(ctx, conn, after, nil, 1, bson.D{
		{"nss", nil},
		{"type", bson.M{"$ne": defs.ExternalBackup}},
		{"store.profile", nil},
	})
}

func getRecentBackup(
	ctx context.Context,
	conn connect.Client,
	after,
	before *primitive.Timestamp,
	sort int,
	opts bson.D,
) (*BackupMeta, error) {
	q := append(bson.D{}, opts...)
	q = append(q, bson.E{"status", defs.StatusDone})
	if after != nil {
		q = append(q, bson.E{"last_write_ts", bson.M{"$gte": after}})
	}
	if before != nil {
		q = append(q, bson.E{"last_write_ts", bson.M{"$lte": before}})
	}

	res := conn.BcpCollection().FindOne(
		ctx,
		q,
		options.FindOne().SetSort(bson.D{{"start_ts", sort}}),
	)
	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.ErrNotFound
		}
		return nil, errors.Wrap(err, "get")
	}

	b := &BackupMeta{}
	err := res.Decode(b)
	return b, errors.Wrap(err, "decode")
}

func FindBaseSnapshotLWAfter(
	ctx context.Context,
	conn connect.Client,
	lw primitive.Timestamp,
) (primitive.Timestamp, error) {
	return findBaseSnapshotLWImpl(ctx, conn, bson.M{"$gt": lw}, 1)
}

func FindBaseSnapshotLWBefore(
	ctx context.Context,
	conn connect.Client,
	lw primitive.Timestamp,
	exclude primitive.Timestamp,
) (primitive.Timestamp, error) {
	return findBaseSnapshotLWImpl(ctx, conn, bson.M{"$lt": lw, "$ne": exclude}, -1)
}

func findBaseSnapshotLWImpl(
	ctx context.Context,
	conn connect.Client,
	lwCond bson.M,
	sort int,
) (primitive.Timestamp, error) {
	f := bson.D{
		{"nss", nil},
		{"type", bson.M{"$ne": defs.ExternalBackup}},
		{"store.profile", nil},
		{"last_write_ts", lwCond},
		{"status", defs.StatusDone},
	}
	o := options.FindOne().
		SetProjection(bson.D{{"last_write_ts", 1}}).
		SetSort(bson.D{{"last_write_ts", sort}})
	res := conn.BcpCollection().FindOne(ctx, f, o)
	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			err = nil
		}
		return primitive.Timestamp{}, errors.Wrap(err, "query")
	}

	bcp := &BackupMeta{}
	err := res.Decode(&bcp)
	return bcp.LastWriteTS, errors.Wrap(err, "decode")
}

func BackupsList(ctx context.Context, conn connect.Client, limit int64) ([]BackupMeta, error) {
	cur, err := conn.BcpCollection().Find(
		ctx,
		bson.M{},
		options.Find().SetLimit(limit).SetSort(bson.D{{"start_ts", -1}}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "query mongo")
	}
	defer cur.Close(ctx)

	backups := []BackupMeta{}
	for cur.Next(ctx) {
		b := BackupMeta{}
		err := cur.Decode(&b)
		if err != nil {
			return nil, errors.Wrap(err, "message decode")
		}
		if b.Type == "" {
			b.Type = defs.LogicalBackup
		}
		backups = append(backups, b)
	}

	return backups, cur.Err()
}

func BackupsDoneList(
	ctx context.Context,
	conn connect.Client,
	after *primitive.Timestamp,
	limit int64,
	order int,
) ([]BackupMeta, error) {
	q := bson.D{{"status", defs.StatusDone}}
	if after != nil {
		q = append(q, bson.E{"last_write_ts", bson.M{"$gte": after}})
	}

	cur, err := conn.BcpCollection().Find(
		ctx,
		q,
		options.Find().SetLimit(limit).SetSort(bson.D{{"last_write_ts", order}}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "query mongo")
	}
	defer cur.Close(ctx)

	backups := []BackupMeta{}
	for cur.Next(ctx) {
		b := BackupMeta{}
		err := cur.Decode(&b)
		if err != nil {
			return nil, errors.Wrap(err, "message decode")
		}
		backups = append(backups, b)
	}

	return backups, cur.Err()
}

func SetRSNomination(ctx context.Context, conn connect.Client, bcpName, rs string) error {
	n := BackupRsNomination{RS: rs, Nodes: []string{}}
	_, err := conn.BcpCollection().
		UpdateOne(
			ctx,
			bson.D{{"name", bcpName}},
			bson.D{{"$addToSet", bson.M{"n": n}}},
		)

	return errors.Wrap(err, "query")
}

func GetRSNominees(
	ctx context.Context,
	conn connect.Client,
	bcpName, rsName string,
) (*BackupRsNomination, error) {
	bcp, err := NewDBManager(conn).GetBackupByName(ctx, bcpName)
	if err != nil {
		return nil, err
	}

	for _, n := range bcp.Nomination {
		if n.RS == rsName {
			return &n, nil
		}
	}

	return nil, errors.ErrNotFound
}

func SetRSNominees(ctx context.Context, conn connect.Client, bcpName, rsName string, nodes []string) error {
	_, err := conn.BcpCollection().UpdateOne(
		ctx,
		bson.D{{"name", bcpName}, {"n.rs", rsName}},
		bson.D{
			{"$set", bson.M{"n.$.n": nodes}},
		},
	)

	return err
}

func SetRSNomineeACK(ctx context.Context, conn connect.Client, bcpName, rsName, node string) error {
	_, err := conn.BcpCollection().UpdateOne(
		ctx,
		bson.D{{"name", bcpName}, {"n.rs", rsName}},
		bson.D{
			{"$set", bson.M{"n.$.ack": node}},
		},
	)

	return err
}
