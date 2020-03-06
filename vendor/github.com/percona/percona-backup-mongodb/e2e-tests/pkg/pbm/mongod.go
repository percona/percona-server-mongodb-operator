package pbm

import (
	"context"
	"math/rand"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	"gopkg.in/mgo.v2/bson"

	"github.com/percona/percona-backup-mongodb/pbm"
)

type Mongo struct {
	cn  *mongo.Client
	ctx context.Context
}

func NewMongo(ctx context.Context, connectionURI string) (*Mongo, error) {
	cn, err := connect(ctx, connectionURI, "e2e-tests")
	if err != nil {
		return nil, errors.Wrap(err, "connect")
	}

	return &Mongo{
		cn:  cn,
		ctx: ctx,
	}, nil
}

func connect(ctx context.Context, uri, appName string) (*mongo.Client, error) {
	client, err := mongo.NewClient(
		options.Client().ApplyURI(uri).
			SetAppName(appName).
			SetReadPreference(readpref.Primary()).
			SetReadConcern(readconcern.Majority()).
			SetWriteConcern(writeconcern.New(writeconcern.WMajority())),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create mongo client")
	}
	err = client.Connect(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "mongo connect")
	}

	err = client.Ping(ctx, nil)
	if err != nil {
		return nil, errors.Wrap(err, "mongo ping")
	}

	return client, nil
}

const (
	testDB = "test"
)

func (m *Mongo) GenBallast(ln int) error {
	return m.GenData("test", "ballast", ln)
}

type TestData struct {
	IDX   int     `bson:"idx"`
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

func (m *Mongo) GenData(db, collection string, ln int) error {
	var data []interface{}
	for i := 0; i < ln; i++ {
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

func genData(idx, strLen int) TestData {
	l1 := make([]byte, strLen)
	l2 := make([]byte, strLen)
	d := make([]int64, strLen)

	for i := 0; i < strLen; i++ {
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

func (m *Mongo) ResetCounters() (int, error) {
	r, err := m.cn.Database(testDB).Collection("counter").DeleteMany(m.ctx, bson.M{})
	if err != nil {
		return 0, err
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

func (m *Mongo) GetIsMaster() (*pbm.IsMaster, error) {
	im := &pbm.IsMaster{}
	err := m.cn.Database("test").RunCommand(m.ctx, bson.M{"isMaster": 1}).Decode(im)
	if err != nil {
		return nil, errors.Wrap(err, "run mongo command isMaster")
	}
	return im, nil
}

func (m *Mongo) GetLastWrite() (primitive.Timestamp, error) {
	isMaster, err := m.GetIsMaster()
	if err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "get isMaster data")
	}
	if isMaster.LastWrite.MajorityOpTime.TS.T == 0 {
		return primitive.Timestamp{}, errors.New("timestamp is nil")
	}
	return isMaster.LastWrite.OpTime.TS, nil
}

func (m *Mongo) Conn() *mongo.Client {
	return m.cn
}
