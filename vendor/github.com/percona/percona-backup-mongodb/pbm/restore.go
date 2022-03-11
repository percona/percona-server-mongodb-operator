package pbm

import (
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type RestoreMeta struct {
	OPID             string              `bson:"opid" json:"opid"`
	Name             string              `bson:"name" json:"name"`
	Backup           string              `bson:"backup" json:"backup"`
	StartPITR        int64               `bson:"start_pitr" json:"start_pitr"`
	PITR             int64               `bson:"pitr" json:"pitr"`
	Replsets         []RestoreReplset    `bson:"replsets" json:"replsets"`
	Hb               primitive.Timestamp `bson:"hb" json:"hb"`
	StartTS          int64               `bson:"start_ts" json:"start_ts"`
	LastTransitionTS int64               `bson:"last_transition_ts" json:"last_transition_ts"`
	Status           Status              `bson:"status" json:"status"`
	Conditions       []Condition         `bson:"conditions" json:"conditions"`
	Error            string              `bson:"error,omitempty" json:"error,omitempty"`
}

type RestoreReplset struct {
	Name             string              `bson:"name" json:"name"`
	StartTS          int64               `bson:"start_ts" json:"start_ts"`
	Status           Status              `bson:"status" json:"status"`
	LastTransitionTS int64               `bson:"last_transition_ts" json:"last_transition_ts"`
	LastWriteTS      primitive.Timestamp `bson:"last_write_ts" json:"last_write_ts"`
	Error            string              `bson:"error,omitempty" json:"error,omitempty"`
	Conditions       []Condition         `bson:"conditions" json:"conditions"`
}

func (p *PBM) SetRestoreMeta(m *RestoreMeta) error {
	m.LastTransitionTS = m.StartTS
	m.Conditions = append(m.Conditions, Condition{
		Timestamp: m.StartTS,
		Status:    m.Status,
	})

	_, err := p.Conn.Database(DB).Collection(RestoresCollection).InsertOne(p.ctx, m)

	return err
}

func (p *PBM) GetRestoreMetaByOPID(opid string) (*RestoreMeta, error) {
	return p.getRestoreMeta(bson.D{{"opid", opid}})
}

func (p *PBM) GetRestoreMeta(name string) (*RestoreMeta, error) {
	return p.getRestoreMeta(bson.D{{"name", name}})
}

func (p *PBM) getRestoreMeta(clause bson.D) (*RestoreMeta, error) {
	res := p.Conn.Database(DB).Collection(RestoresCollection).FindOne(p.ctx, clause)
	if res.Err() != nil {
		if res.Err() == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, errors.Wrap(res.Err(), "get")
	}
	r := &RestoreMeta{}
	err := res.Decode(r)
	return r, errors.Wrap(err, "decode")
}

// GetLastRestore returns last successfully finished restore
// and nil if there is no such restore yet.
func (p *PBM) GetLastRestore() (*RestoreMeta, error) {
	r := new(RestoreMeta)

	res := p.Conn.Database(DB).Collection(RestoresCollection).FindOne(
		p.ctx,
		bson.D{{"status", StatusDone}},
		options.FindOne().SetSort(bson.D{{"start_ts", -1}}),
	)
	if res.Err() != nil {
		if res.Err() == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, errors.Wrap(res.Err(), "get")
	}
	err := res.Decode(r)
	return r, errors.Wrap(err, "decode")
}

func (p *PBM) AddRestoreRSMeta(name string, rs RestoreReplset) error {
	rs.LastTransitionTS = rs.StartTS
	rs.Conditions = append(rs.Conditions, Condition{
		Timestamp: rs.StartTS,
		Status:    rs.Status,
	})
	_, err := p.Conn.Database(DB).Collection(RestoresCollection).UpdateOne(
		p.ctx,
		bson.D{{"name", name}},
		bson.D{{"$addToSet", bson.M{"replsets": rs}}},
	)

	return err
}

func (p *PBM) RestoreHB(name string) error {
	ts, err := p.ClusterTime()
	if err != nil {
		return errors.Wrap(err, "read cluster time")
	}

	_, err = p.Conn.Database(DB).Collection(RestoresCollection).UpdateOne(
		p.ctx,
		bson.D{{"name", name}},
		bson.D{
			{"$set", bson.M{"hb": ts}},
		},
	)

	return errors.Wrap(err, "write into db")
}

func (p *PBM) ChangeRestoreStateOPID(opid string, s Status, msg string) error {
	return p.changeRestoreState(bson.D{{"name", opid}}, s, msg)
}

func (p *PBM) ChangeRestoreState(name string, s Status, msg string) error {
	return p.changeRestoreState(bson.D{{"name", name}}, s, msg)
}

func (p *PBM) changeRestoreState(clause bson.D, s Status, msg string) error {
	ts := time.Now().UTC().Unix()
	_, err := p.Conn.Database(DB).Collection(RestoresCollection).UpdateOne(
		p.ctx,
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

func (p *PBM) SetRestoreBackup(name, backupName string) error {
	_, err := p.Conn.Database(DB).Collection(RestoresCollection).UpdateOne(
		p.ctx,
		bson.D{{"name", name}},
		bson.D{
			{"$set", bson.M{"backup": backupName}},
		},
	)

	return err
}

func (p *PBM) SetRestorePITR(name string, ts int64) error {
	_, err := p.Conn.Database(DB).Collection(RestoresCollection).UpdateOne(
		p.ctx,
		bson.D{{"name", name}},
		bson.D{
			{"$set", bson.M{"pitr": ts}},
		},
	)

	return err
}

func (p *PBM) SetOplogTimestamps(name string, start, end int64) error {
	_, err := p.Conn.Database(DB).Collection(RestoresCollection).UpdateOne(
		p.ctx,
		bson.M{"name": name},
		bson.M{"$set": bson.M{"start_pitr": start, "pitr": end}},
	)

	return err
}

func (p *PBM) ChangeRestoreRSState(name string, rsName string, s Status, msg string) error {
	ts := time.Now().UTC().Unix()
	_, err := p.Conn.Database(DB).Collection(RestoresCollection).UpdateOne(
		p.ctx,
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

func (p *PBM) RestoresList(limit int64) ([]RestoreMeta, error) {
	cur, err := p.Conn.Database(DB).Collection(RestoresCollection).Find(
		p.ctx,
		bson.M{},
		options.Find().SetLimit(limit).SetSort(bson.D{{"start_ts", -1}}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "query mongo")
	}

	defer cur.Close(p.ctx)

	restores := []RestoreMeta{}
	for cur.Next(p.ctx) {
		r := RestoreMeta{}
		err := cur.Decode(&r)
		if err != nil {
			return nil, errors.Wrap(err, "message decode")
		}
		restores = append(restores, r)
	}

	return restores, cur.Err()
}
