package oplog

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
)

// PITRMeta contains all operational data about PITR execution process.
type PITRMeta struct {
	StartTS    int64               `bson:"start_ts" json:"start_ts"`
	Hb         primitive.Timestamp `bson:"hb" json:"hb"`
	Status     Status              `bson:"status" json:"status"`
	Nomination []PITRNomination    `bson:"n" json:"n"`
	Replsets   []PITRReplset       `bson:"replsets" json:"replsets"`
}

// PITRNomination is used to choose (nominate and elect) member(s)
// which will perform PITR process within a replica set(s).
type PITRNomination struct {
	RS    string   `bson:"rs" json:"rs"`
	Nodes []string `bson:"n" json:"n"`
	Ack   string   `bson:"ack" json:"ack"`
}

// PITRReplset holds status for each replica set.
// Each replicaset tries to reach cluster status set by Cluser Leader.
type PITRReplset struct {
	Name   string `bson:"name" json:"name"`
	Node   string `bson:"node" json:"node"`
	Status Status `bson:"status" json:"status"`
	Error  string `bson:"error,omitempty" json:"error,omitempty"`
}

// Status is a PITR status.
// It is used within pbmPITR collection to sync operation between
// cluster leader and agents.
type Status string

const (
	StatusReady    Status = "ready"
	StatusRunning  Status = "running"
	StatusReconfig Status = "reconfig"
	StatusError    Status = "error"
	StatusUnset    Status = ""
)

// Init add initial PITR document.
func InitMeta(ctx context.Context, conn connect.Client) error {
	ts, err := topo.GetClusterTime(ctx, conn)
	if err != nil {
		return errors.Wrap(err, "init pitr meta, read cluster time")
	}

	pitrMeta := PITRMeta{
		StartTS:    time.Now().Unix(),
		Nomination: []PITRNomination{},
		Replsets:   []PITRReplset{},
		Hb:         ts,
	}
	_, err = conn.PITRCollection().ReplaceOne(
		ctx,
		bson.D{},
		pitrMeta,
		options.Replace().SetUpsert(true),
	)

	return errors.Wrap(err, "pitr meta replace")
}

// GetMeta fetches PITR meta doc from pbmPITR collection.
func GetMeta(
	ctx context.Context,
	conn connect.Client,
) (*PITRMeta, error) {
	res := conn.PITRCollection().FindOne(ctx, bson.D{})
	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.ErrNotFound
		}
		return nil, errors.Wrap(err, "find pitr meta")
	}

	meta := &PITRMeta{}
	if err := res.Decode(meta); err != nil {
		return nil, errors.Wrap(err, "decode")
	}
	return meta, nil
}

// SetClusterStatus sets cluster status field of PITR Meta doc.
// It also resets all content of replsets field doc.
func SetClusterStatus(ctx context.Context, conn connect.Client, status Status) error {
	_, err := conn.PITRCollection().
		UpdateOne(
			ctx,
			bson.D{},
			bson.D{{"$set", bson.M{
				"status":   status,
				"replsets": []PITRReplset{},
			}}},
			options.Update().SetUpsert(true),
		)
	return errors.Wrap(err, "update pitr doc to status")
}

func GetClusterStatus(ctx context.Context, conn connect.Client) (Status, error) {
	meta, err := GetMeta(ctx, conn)
	if err != nil {
		if errors.Is(err, errors.ErrNotFound) {
			return StatusUnset, err
		}
		return StatusUnset, errors.Wrap(err, "getting meta")
	}

	return meta.Status, nil
}

// SetReadyRSStatus sets Ready status for specified replicaset.
func SetReadyRSStatus(ctx context.Context, conn connect.Client, rs, node string) error {
	repliset := PITRReplset{
		Name:   rs,
		Node:   node,
		Status: StatusReady,
	}
	_, err := conn.PITRCollection().
		UpdateOne(
			ctx,
			bson.D{},
			bson.D{{"$addToSet", bson.M{"replsets": repliset}}},
			options.Update().SetUpsert(true),
		)
	return errors.Wrap(err, "update pitr doc for RS ready status")
}

// SetErrorRSStatus sets Error status for specified replicaset and includes error descrioption.
func SetErrorRSStatus(ctx context.Context, conn connect.Client, rs, node, errText string) error {
	repliset := PITRReplset{
		Name:   rs,
		Node:   node,
		Status: StatusError,
		Error:  errText,
	}
	_, err := conn.PITRCollection().
		UpdateOne(
			ctx,
			bson.D{},
			bson.D{{"$addToSet", bson.M{"replsets": repliset}}},
			options.Update().SetUpsert(true),
		)
	return errors.Wrap(err, "update pitr doc for RS error status")
}

// GetReplSetsWithStatus fetches all replica sets which reported status specified with parameter.
func GetReplSetsWithStatus(ctx context.Context, conn connect.Client, status Status) ([]PITRReplset, error) {
	meta, err := GetMeta(ctx, conn)
	if err != nil {
		return nil, errors.Wrap(err, "get meta")
	}

	replSetsWithStatus := []PITRReplset{}
	for _, rs := range meta.Replsets {
		if rs.Status == status {
			replSetsWithStatus = append(replSetsWithStatus, rs)
		}
	}
	return replSetsWithStatus, nil
}

// SetPITRNomination adds nomination fragment for specified RS within PITRMeta.
func SetPITRNomination(ctx context.Context, conn connect.Client, rs string) error {
	n := PITRNomination{
		RS:    rs,
		Nodes: []string{},
	}
	_, err := conn.PITRCollection().
		UpdateOne(
			ctx,
			bson.D{},
			bson.D{{"$addToSet", bson.M{"n": n}}},
			options.Update().SetUpsert(true),
		)
	return errors.Wrap(err, "update pitr nomination")
}

// GetPITRNominees fetches nomination fragment for specified RS
// from pmbPITR document.
// If document is not found, or document fragment for specific RS is not found,
// error ErrNotFound is returned.
func GetPITRNominees(
	ctx context.Context,
	conn connect.Client,
	rs string,
) (*PITRNomination, error) {
	meta, err := GetMeta(ctx, conn)
	if err != nil {
		return nil, errors.Wrap(err, "get meta")
	}

	for _, n := range meta.Nomination {
		if n.RS == rs {
			return &n, nil
		}
	}

	return nil, errors.ErrNotFound
}

// SetPITRNominees add nominee(s) for specific RS.
// It is used by cluster leader within nomination process.
func SetPITRNominees(
	ctx context.Context,
	conn connect.Client,
	rs string,
	nodes []string,
) error {
	_, err := conn.PITRCollection().UpdateOne(
		ctx,
		bson.D{
			{"n.rs", rs},
		},
		bson.D{
			{"$addToSet", bson.M{"n.$.n": bson.M{"$each": nodes}}},
		},
	)

	return errors.Wrap(err, "update pitr nominees")
}

// SetPITRNomineeACK add ack for specific nomination.
// It is used by nominee, after the nomination is created by cluster leader.
func SetPITRNomineeACK(
	ctx context.Context,
	conn connect.Client,
	rs,
	node string,
) error {
	_, err := conn.PITRCollection().UpdateOne(
		ctx,
		bson.D{{"n.rs", rs}},
		bson.D{
			{"$set", bson.M{"n.$.ack": node}},
		},
	)

	return errors.Wrap(err, "update pitr nominee ack")
}

// GetAgentsWithACK returns the list of all acknowledged agents.
func GetAgentsWithACK(ctx context.Context, conn connect.Client) ([]string, error) {
	agents := []string{}
	meta, err := GetMeta(ctx, conn)
	if err != nil {
		if errors.Is(err, errors.ErrNotFound) {
			return agents, err
		}
		return agents, errors.Wrap(err, "getting meta")
	}

	for _, n := range meta.Nomination {
		if len(n.Ack) > 0 {
			agents = append(agents, fmt.Sprintf("%s/%s", n.RS, n.Ack))
		}
	}

	return agents, nil
}

func SetHbForPITR(ctx context.Context, conn connect.Client) error {
	ts, err := topo.GetClusterTime(ctx, conn)
	if err != nil {
		return errors.Wrap(err, "read cluster time")
	}

	_, err = conn.PITRCollection().UpdateOne(
		ctx,
		bson.D{},
		bson.D{
			{"$set", bson.M{"hb": ts}},
		},
	)

	return errors.Wrap(err, "update pbmPITR")
}
