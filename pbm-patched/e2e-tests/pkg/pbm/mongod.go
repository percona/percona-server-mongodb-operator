package pbm

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
)

type Mongo struct {
	cn  *mongo.Client
	ctx context.Context
}

func NewMongo(ctx context.Context, connectionURI string) (*Mongo, error) {
	cn, err := connect.MongoConnect(ctx, connectionURI, connect.AppName("e2e-tests"))
	if err != nil {
		return nil, errors.Wrap(err, "connect")
	}

	return &Mongo{cn: cn, ctx: ctx}, nil
}

const (
	testDB = "test"
)

func (m *Mongo) GenBallast(ln int64) error {
	return m.GenData("test", "ballast", 0, ln)
}

type TestData struct {
	IDX   int64   `bson:"idx"`
	Num   []int64 `bson:"num"`
	Data1 []byte  `bson:"data1"`
	Data2 []byte  `bson:"data2"`
	C     int     `bson:"changed"`
}

func (m *Mongo) ServerVersion() (string, error) {
	v := struct {
		V string `bson:"version"`
	}{}
	err := m.cn.Database("admin").RunCommand(
		m.ctx,
		bson.M{"buildInfo": 1},
	).Decode(&v)

	return v.V, err
}

func (m *Mongo) GenData(db, collection string, start, ln int64) error {
	var data []interface{}
	for i := start; i < ln; i++ {
		data = append(data, genData(i, 64))
		if i%100 == 0 {
			_, err := m.cn.Database(db).Collection(collection).InsertMany(m.ctx, data)
			if err != nil {
				return err
			}
			data = data[:0]
		}
	}
	if len(data) > 0 {
		_, err := m.cn.Database(db).Collection(collection).InsertMany(m.ctx, data)
		return err
	}

	return nil
}

func genData(idx, strLen int64) TestData {
	l1 := make([]byte, strLen)
	l2 := make([]byte, strLen)
	d := make([]int64, strLen)

	for i := int64(0); i < strLen; i++ {
		d[i] = rand.Int63()
		l1[i] = byte(d[i]&25 + 'a')
		l2[i] = byte(d[i]&25 + 'A')
	}

	return TestData{
		IDX:   idx,
		Num:   d,
		Data1: l1,
		Data2: l2,
		C:     -1,
	}
}

func (m *Mongo) ResetBallast() (int, error) {
	r, err := m.cn.Database(testDB).Collection("ballast").DeleteMany(m.ctx, bson.M{})
	if err != nil {
		return 0, err
	}
	return int(r.DeletedCount), nil
}

func (m *Mongo) CreateTS(name string) error {
	return m.cn.Database(testDB).RunCommand(
		m.ctx,
		bson.D{{"create", name}, {"timeseries", bson.M{"timeField": "timestamp"}}},
	).Err()
}

func (m *Mongo) InsertTS(col string) error {
	_, err := m.cn.Database(testDB).Collection(col).InsertOne(m.ctx, bson.M{"timestamp": time.Now(), "x": rand.Int()})
	return err
}

func (m *Mongo) Count(col string) (int64, error) {
	return m.cn.Database(testDB).Collection(col).CountDocuments(m.ctx, bson.M{})
}

func (m *Mongo) Drop(col string) error {
	return m.cn.Database(testDB).Collection(col).Drop(m.ctx)
}

// SetBallast sets ballast documents amount to be equal to given `size`
// it removes execive documents or creates new if needed
func (m *Mongo) SetBallast(size int64) (int64, error) {
	r, err := m.cn.Database(testDB).Collection("ballast").CountDocuments(m.ctx, bson.M{})
	if err != nil {
		return 0, errors.Wrap(err, "count")
	}

	size -= r
	switch {
	case size == 0:
		return r, nil
	case size < 0:
		_, err := m.cn.Database(testDB).Collection("ballast").DeleteMany(m.ctx, bson.M{"idx": bson.M{"$gt": size * -1}})
		if err != nil {
			return 0, errors.Wrap(err, "delete")
		}
	case size > 0:
		err := m.GenData("test", "ballast", r, size)
		if err != nil {
			return 0, errors.Wrap(err, "add")
		}
	}

	return m.cn.Database(testDB).Collection("ballast").CountDocuments(m.ctx, bson.M{})
}

func (m *Mongo) DBhashes() (map[string]string, error) {
	r := m.cn.Database(testDB).RunCommand(m.ctx, bson.M{"dbHash": 1})
	if r.Err() != nil {
		return nil, errors.Wrap(r.Err(), "run command")
	}
	h := struct {
		C map[string]string `bson:"collections"`
		M string            `bson:"md5"`
	}{}
	err := r.Decode(&h)
	h.C["_all_"] = h.M

	return h.C, errors.Wrap(err, "decode")
}

type Counter struct {
	Count     int
	WallTime  time.Time
	ID        interface{}
	WriteTime primitive.Timestamp
}

func (c Counter) String() string {
	return fmt.Sprintf("cnt: %d, clusterT: %d/%d (%v), wallT: %v",
		c.Count,
		c.WriteTime.T,
		c.WriteTime.I,
		time.Unix(int64(c.WriteTime.T), 0),
		c.WallTime,
	)
}

func (m *Mongo) ResetCounters() (int, error) {
	r, err := m.cn.Database(testDB).Collection("counter").DeleteMany(m.ctx, bson.M{})
	if err != nil {
		// workaround for 'sharding status of collection is not currently known' error
		r1, err1 := m.cn.Database(testDB).Collection("counter").CountDocuments(m.ctx, bson.M{})
		err2 := m.cn.Database(testDB).Collection("counter").Drop(m.ctx)
		if err1 == nil && err2 == nil {
			return int(r1), nil
		}
		return 0, err2
	}
	return int(r.DeletedCount), nil
}

func (m *Mongo) WriteCounter(i int) (*Counter, error) {
	ins := struct {
		IDX  int       `bson:"idx"`
		Time time.Time `bson:"time"`
		TS   int64     `bson:"ts"`
	}{
		IDX:  i,
		Time: time.Now(),
	}

	ins.TS = ins.Time.Unix()
	r, err := m.cn.Database(testDB).Collection("counter").InsertOne(m.ctx, ins)
	if err != nil {
		return nil, err
	}

	return &Counter{
		Count:    i,
		ID:       r.InsertedID,
		WallTime: ins.Time,
	}, nil
}

func (m *Mongo) GetCounters() ([]Counter, error) {
	cur, err := m.cn.Database(testDB).Collection("counter").Find(
		m.ctx,
		bson.M{},
		options.Find().SetSort(bson.M{"idx": 1}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create cursor failed")
	}
	defer cur.Close(m.ctx)

	var data []Counter
	for cur.Next(m.ctx) {
		t := struct {
			ID   interface{} `bson:"_id"`
			IDX  int         `bson:"idx"`
			Time time.Time   `bson:"time"`
			TS   int64       `bson:"ts"`
		}{}
		err := cur.Decode(&t)
		if err != nil {
			return nil, errors.Wrap(err, "decode data")
		}
		data = append(data, Counter{
			Count:    t.IDX,
			ID:       t.ID,
			WallTime: time.Unix(t.TS, 0),
		})
	}

	return data, nil
}

func (m *Mongo) GetNodeInfo() (*topo.NodeInfo, error) {
	inf := &topo.NodeInfo{}
	err := m.cn.Database("test").RunCommand(m.ctx, bson.M{"hello": 1}).Decode(inf)
	if err != nil {
		return nil, errors.Wrap(err, "run mongo command")
	}
	return inf, nil
}

func (m *Mongo) GetLastWrite() (primitive.Timestamp, error) {
	inf, err := m.GetNodeInfo()
	if err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "get NodeInfo data")
	}
	if inf.LastWrite.MajorityOpTime.TS.T == 0 {
		return primitive.Timestamp{}, errors.New("timestamp is nil")
	}
	return inf.LastWrite.OpTime.TS, nil
}

func (m *Mongo) Conn() *mongo.Client {
	return m.cn
}
