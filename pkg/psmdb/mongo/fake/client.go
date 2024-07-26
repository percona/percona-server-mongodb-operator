package fake

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	mgo "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

type fakeMongoClient struct{}

func NewClient() mongo.Client {
	return &fakeMongoClient{}
}

func (c *fakeMongoClient) Disconnect(ctx context.Context) error {
	return nil
}

type fakeMongoClientDatabase struct{}

func (c *fakeMongoClientDatabase) RunCommand(ctx context.Context, runCommand interface{}, opts ...*options.RunCmdOptions) *mgo.SingleResult {
	return singleResult(mongo.OKResponse{
		OK: 1,
	})
}

func singleResult(doc any) *mgo.SingleResult {
	bsonData, err := bson.Marshal(doc)
	if err != nil {
		return mgo.NewSingleResultFromDocument(bson.D{}, err, nil)
	}
	var bsonD bson.D
	err = bson.Unmarshal(bsonData, &bsonD)
	if err != nil {
		return mgo.NewSingleResultFromDocument(bson.D{}, err, nil)
	}
	return mgo.NewSingleResultFromDocument(bsonD, nil, nil)
}

func (c *fakeMongoClient) Database(name string, opts ...*options.DatabaseOptions) mongo.ClientDatabase {
	return new(fakeMongoClientDatabase)
}

func (c *fakeMongoClient) Ping(ctx context.Context, rp *readpref.ReadPref) error {
	return nil
}

func (c *fakeMongoClient) SetDefaultRWConcern(ctx context.Context, readConcern, writeConcern string) error {
	return nil
}

func (c *fakeMongoClient) ReadConfig(ctx context.Context) (mongo.RSConfig, error) {
	return mongo.RSConfig{}, nil
}

func (c *fakeMongoClient) CreateRole(ctx context.Context, role string, privileges []mongo.RolePrivilege, roles []interface{}) error {
	return nil
}

func (c *fakeMongoClient) UpdateRole(ctx context.Context, role string, privileges []mongo.RolePrivilege, roles []interface{}) error {
	return nil
}

func (c *fakeMongoClient) GetRole(ctx context.Context, role string) (*mongo.Role, error) {
	return nil, nil
}

func (c *fakeMongoClient) CreateUser(ctx context.Context, db, user, pwd string, roles ...map[string]interface{}) error {
	return nil
}

func (c *fakeMongoClient) AddShard(ctx context.Context, rsName, host string) error {
	return nil
}

func (c *fakeMongoClient) WriteConfig(ctx context.Context, cfg mongo.RSConfig) error {
	return nil
}

func (c *fakeMongoClient) RSStatus(ctx context.Context) (mongo.Status, error) {
	return mongo.Status{}, nil
}

func (c *fakeMongoClient) StartBalancer(ctx context.Context) error {
	return nil
}

func (c *fakeMongoClient) StopBalancer(ctx context.Context) error {
	return nil
}

func (c *fakeMongoClient) IsBalancerRunning(ctx context.Context) (bool, error) {
	return false, nil
}

func (c *fakeMongoClient) GetFCV(ctx context.Context) (string, error) {
	return "", nil
}

func (c *fakeMongoClient) SetFCV(ctx context.Context, version string) error {
	return nil
}

func (c *fakeMongoClient) ListDBs(ctx context.Context) (mongo.DBList, error) {
	return mongo.DBList{}, nil
}

func (c *fakeMongoClient) ListShard(ctx context.Context) (mongo.ShardList, error) {
	return mongo.ShardList{}, nil
}

func (c *fakeMongoClient) RemoveShard(ctx context.Context, shard string) (mongo.ShardRemoveResp, error) {
	return mongo.ShardRemoveResp{}, nil
}

func (c *fakeMongoClient) RSBuildInfo(ctx context.Context) (mongo.BuildInfo, error) {
	return mongo.BuildInfo{}, nil
}

func (c *fakeMongoClient) StepDown(ctx context.Context, seconds int, force bool) error {
	return nil
}

func (c *fakeMongoClient) Freeze(ctx context.Context, seconds int) error {
	return nil
}

func (c *fakeMongoClient) IsMaster(ctx context.Context) (*mongo.IsMasterResp, error) {
	return nil, nil
}

func (c *fakeMongoClient) GetUserInfo(ctx context.Context, username string) (*mongo.User, error) {
	return nil, nil
}

func (c *fakeMongoClient) UpdateUserRoles(ctx context.Context, db, username string, roles []map[string]interface{}) error {
	return nil
}

func (c *fakeMongoClient) UpdateUserPass(ctx context.Context, db, name, pass string) error {
	return nil
}

func (c *fakeMongoClient) UpdateUser(ctx context.Context, currName, newName, pass string) error {
	return nil
}
