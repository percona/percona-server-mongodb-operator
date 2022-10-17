package mongo

//go:generate mockgen -aux_files github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo=sharder.go,github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo=user_manager.go,github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo=rs_config_manager.go,github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo=topology_manager.go -destination=mocks/client.go -source=client.go

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("mongo")

type Config struct {
	Hosts       []string
	ReplSetName string
	Username    string
	Password    string
	TLSConf     *tls.Config
	Direct      bool
}

type client struct {
	config *Config
	conn   *mongo.Client
}

type Client interface {
	Sharder
	UserManager
	RSConfigManager
	TopologyManager

	Dial() error
	Ping(ctx context.Context, pref *readpref.ReadPref) error
	Disconnect(ctx context.Context) error
	GetFCV(ctx context.Context) (string, error)
	SetFCV(ctx context.Context, version string) error
	ListDatabases(ctx context.Context, options ...bson.E) (DBList, error)
	RSBuildInfo(ctx context.Context) (BuildInfo, error)
}

func New(conf *Config) Client {
	return &client{config: conf}
}

func (c *client) Dial() error {
	ctx, connectcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer connectcancel()

	opts := options.Client().
		SetHosts(c.config.Hosts).
		SetReplicaSet(c.config.ReplSetName).
		SetAuth(options.Credential{
			Password: c.config.Password,
			Username: c.config.Username,
		}).
		SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))).
		SetReadPreference(readpref.Primary()).SetTLSConfig(c.config.TLSConf).
		SetDirect(c.config.Direct).
		SetConnectTimeout(10 * time.Second).
		SetServerSelectionTimeout(10 * time.Second)

	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return errors.Wrap(err, "connect to mongo rs")
	}

	defer func() {
		if err != nil {
			derr := client.Disconnect(ctx)
			if derr != nil {
				log.Error(err, "failed to disconnect")
			}
		}
	}()

	ctx, pingcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pingcancel()

	err = client.Ping(ctx, readpref.Primary())
	if err != nil {
		return errors.Wrap(err, "ping mongo")
	}

	c.conn = client

	return nil
}

func (c *client) Ping(ctx context.Context, pref *readpref.ReadPref) error {
	return c.conn.Ping(ctx, pref)
}

func (c *client) Disconnect(ctx context.Context) error {
	return c.conn.Disconnect(ctx)
}

func (c *client) SetDefaultRWConcern(ctx context.Context, readConcern, writeConcern string) error {
	cmd := bson.D{
		{Key: "setDefaultRWConcern", Value: 1},
		{Key: "defaultReadConcern", Value: bson.D{{Key: "level", Value: readConcern}}},
		{Key: "defaultWriteConcern", Value: bson.D{{Key: "w", Value: writeConcern}}},
	}

	res := c.conn.Database("admin").RunCommand(ctx, cmd)
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "setDefaultRWConcern")
	}

	return nil
}

func (c *client) GetFCV(ctx context.Context) (string, error) {
	res := FCV{}

	resp := c.conn.Database("admin").RunCommand(ctx, bson.D{
		{Key: "getParameter", Value: 1},
		{Key: "featureCompatibilityVersion", Value: 1},
	})

	if err := resp.Decode(&res); err != nil {
		return "", errors.Wrap(err, "failed to decode balancer status response")
	}

	if res.OK != 1 {
		return "", errors.Errorf("mongo says: %s", res.Errmsg)
	}

	return res.FCV.Version, nil
}

func (c *client) SetFCV(ctx context.Context, version string) error {
	res := OKResponse{}

	command := "setFeatureCompatibilityVersion"

	resp := c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: command, Value: version}})
	if resp.Err() != nil {
		return errors.Wrap(resp.Err(), command)
	}

	if err := resp.Decode(&res); err != nil {
		return errors.Wrapf(err, "failed to decode %v response", *resp)
	}

	if res.OK != 1 {
		return errors.Errorf("mongo says: %s", res.Errmsg)
	}

	return nil
}

func (c *client) ListDatabases(ctx context.Context, options ...bson.E) (DBList, error) {
	dbList := DBList{}

	cmd := bson.D{{Key: "listDatabases", Value: 1}}
	cmd = append(cmd, options...)

	resp := c.conn.Database("admin").RunCommand(ctx, cmd)
	if resp.Err() != nil {
		return dbList, errors.Wrap(resp.Err(), "listDatabases")
	}

	if err := resp.Decode(&dbList); err != nil {
		return dbList, errors.Wrap(err, "failed to decode db list")
	}

	if dbList.OK != 1 {
		return dbList, errors.Errorf("mongo says: %s", dbList.Errmsg)
	}

	return dbList, nil
}

func (c *client) RSBuildInfo(ctx context.Context) (BuildInfo, error) {
	bi := BuildInfo{}

	resp := c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: "buildinfo", Value: 1}})
	if resp.Err() != nil {
		return bi, errors.Wrap(resp.Err(), "buildinfo")
	}

	if err := resp.Decode(&bi); err != nil {
		return bi, errors.Wrap(err, "failed to decode build info")
	}

	if bi.OK != 1 {
		return bi, errors.Errorf("mongo says: %s", bi.Errmsg)
	}

	return bi, nil
}
