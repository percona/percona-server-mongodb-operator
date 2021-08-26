package mongo

import (
	"context"
	"crypto/tls"
	"fmt"
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
}

func Dial(conf *Config) (*mongo.Client, error) {
	ctx, connectcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer connectcancel()

	opts := options.Client().
		SetHosts(conf.Hosts).
		SetReplicaSet(conf.ReplSetName).
		SetAuth(options.Credential{
			Password: conf.Password,
			Username: conf.Username,
		}).
		SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))).
		SetReadPreference(readpref.Primary()).SetTLSConfig(conf.TLSConf)

	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, errors.Errorf("failed to connect to mongo rs: %v", err)
	}

	defer func() {
		if err != nil {
			derr := client.Disconnect(ctx)
			if derr != nil {
				log.Error(err, "failed to disconnect")
			}
		}
	}()

	ctx, pingcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pingcancel()

	err = client.Ping(ctx, readpref.Primary())
	if err != nil {
		return nil, errors.Errorf("failed to ping mongo: %v", err)
	}

	return client, nil
}

func ReadConfig(ctx context.Context, client *mongo.Client) (RSConfig, error) {
	resp := ReplSetGetConfig{}
	res := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetConfig", Value: 1}})
	if res.Err() != nil {
		return RSConfig{}, errors.Wrap(res.Err(), "replSetGetConfig")
	}
	if err := res.Decode(&resp); err != nil {
		return RSConfig{}, errors.Wrap(err, "failed to decode to replSetGetConfig")
	}

	if resp.Config == nil {
		return RSConfig{}, errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return *resp.Config, nil
}

func CreateRole(ctx context.Context, client *mongo.Client, role string, privileges []interface{}, roles []interface{}) error {
	resp := OKResponse{}

	privilegesArr := bson.A{}
	for _, p := range privileges {
		privilegesArr = append(privilegesArr, p)
	}

	rolesArr := bson.A{}
	for _, r := range roles {
		rolesArr = append(rolesArr, r)
	}

	m := bson.D{
		{Key: "createRole", Value: role},
		{Key: "privileges", Value: privilegesArr},
		{Key: "roles", Value: rolesArr},
	}

	res := client.Database("admin").RunCommand(context.Background(), m)
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "failed to create role")
	}

	err := res.Decode(&resp)
	if err != nil {
		return errors.Wrap(err, "failed to decode response")
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func CreateUser(ctx context.Context, client *mongo.Client, user, pwd string, roles ...interface{}) error {
	resp := OKResponse{}

	res := client.Database("admin").RunCommand(context.Background(), bson.D{
		{Key: "createUser", Value: user},
		{Key: "pwd", Value: pwd},
		{Key: "roles", Value: roles},
	})
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "failed to create user")
	}

	err := res.Decode(&resp)
	if err != nil {
		return errors.Wrap(err, "failed to decode response")
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func AddShard(ctx context.Context, client *mongo.Client, rsName, host string) error {
	resp := OKResponse{}

	res := client.Database("admin").RunCommand(ctx, bson.D{{Key: "addShard", Value: rsName + "/" + host}})
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "add shard")
	}

	if err := res.Decode(&resp); err != nil {
		return errors.Wrap(err, "failed to decode addShard response")
	}

	if resp.OK != 1 {
		return errors.Errorf("add shard: %s", resp.Errmsg)
	}

	return nil
}

func WriteConfig(ctx context.Context, client *mongo.Client, cfg RSConfig) error {
	resp := OKResponse{}

	// Using force flag since mongo 4.4 forbids to add multiple members at a time.
	res := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetReconfig", Value: cfg}, {Key: "force", Value: true}})
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "replSetReconfig")
	}

	if err := res.Decode(&resp); err != nil {
		return errors.Wrap(err, "failed to decode to replSetReconfigResponse")
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func RSStatus(ctx context.Context, client *mongo.Client) (Status, error) {
	status := Status{}

	resp := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}})
	if resp.Err() != nil {
		return status, errors.Wrap(resp.Err(), "replSetGetStatus")
	}

	if err := resp.Decode(&status); err != nil {
		return status, errors.Wrap(err, "failed to decode rs status")
	}

	if status.OK != 1 {
		return status, errors.Errorf("mongo says: %s", status.Errmsg)
	}

	return status, nil
}

func StartBalancer(ctx context.Context, client *mongo.Client) error {
	return switchBalancer(ctx, client, "balancerStart")
}

func StopBalancer(ctx context.Context, client *mongo.Client) error {
	return switchBalancer(ctx, client, "balancerStop")
}

func switchBalancer(ctx context.Context, client *mongo.Client, command string) error {
	res := OKResponse{}

	resp := client.Database("admin").RunCommand(ctx, bson.D{{Key: command, Value: 1}})
	if resp.Err() != nil {
		return errors.Wrap(resp.Err(), command)
	}

	if err := resp.Decode(&res); err != nil {
		return errors.Wrapf(err, "failed to decode %s response", command)
	}

	if res.OK != 1 {
		return errors.Errorf("mongo says: %s", res.Errmsg)
	}

	return nil
}

func IsBalancerRunning(ctx context.Context, client *mongo.Client) (bool, error) {
	res := BalancerStatus{}

	resp := client.Database("admin").RunCommand(ctx, bson.D{{Key: "balancerStatus", Value: 1}})
	if resp.Err() != nil {
		return false, errors.Wrap(resp.Err(), "balancer status")
	}

	if err := resp.Decode(&res); err != nil {
		return false, errors.Wrap(err, "failed to decode balancer status response")
	}

	if res.OK != 1 {
		return false, errors.Errorf("mongo says: %s", res.Errmsg)
	}

	return res.Mode == "full", nil
}

func GetFCV(ctx context.Context, client *mongo.Client) (string, error) {
	res := FCV{}

	resp := client.Database("admin").RunCommand(ctx, bson.D{
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

func SetFCV(ctx context.Context, client *mongo.Client, version string) error {
	res := OKResponse{}

	command := "setFeatureCompatibilityVersion"

	resp := client.Database("admin").RunCommand(ctx, bson.D{{Key: command, Value: version}})
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

func ListDBs(ctx context.Context, client *mongo.Client) (DBList, error) {
	dbList := DBList{}

	resp := client.Database("admin").RunCommand(ctx, bson.D{{Key: "listDatabases", Value: 1}})
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

func ListShard(ctx context.Context, client *mongo.Client) (ShardList, error) {
	shardList := ShardList{}

	resp := client.Database("admin").RunCommand(ctx, bson.D{{Key: "listShards", Value: 1}})
	if resp.Err() != nil {
		return shardList, errors.Wrap(resp.Err(), "listShards")
	}

	if err := resp.Decode(&shardList); err != nil {
		return shardList, errors.Wrap(err, "failed to decode shard list")
	}

	if shardList.OK != 1 {
		return shardList, errors.Errorf("mongo says: %s", shardList.Errmsg)
	}

	return shardList, nil
}

func RemoveShard(ctx context.Context, client *mongo.Client, shard string) (ShardRemoveResp, error) {
	removeResp := ShardRemoveResp{}

	resp := client.Database("admin").RunCommand(ctx, bson.D{{Key: "removeShard", Value: shard}})
	if resp.Err() != nil {
		return removeResp, errors.Wrap(resp.Err(), "remove shard")
	}

	if err := resp.Decode(&removeResp); err != nil {
		return removeResp, errors.Wrap(err, "failed to decode shard list")
	}

	if removeResp.OK != 1 {
		return removeResp, errors.Errorf("mongo says: %s", removeResp.Errmsg)
	}

	return removeResp, nil
}

func RSBuildInfo(ctx context.Context, client *mongo.Client) (BuildInfo, error) {
	bi := BuildInfo{}

	resp := client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildinfo", Value: 1}})
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

func StepDown(ctx context.Context, client *mongo.Client, force bool) error {
	resp := OKResponse{}

	res := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetStepDown", Value: 60}, {Key: "force", Value: force}})
	err := res.Err()
	if err != nil {
		cErr, ok := err.(mongo.CommandError)
		if ok && cErr.HasErrorLabel("NetworkError") {
			// https://docs.mongodb.com/manual/reference/method/rs.stepDown/#client-connections
			return nil
		}
		return errors.Wrap(err, "replSetStepDown")
	}

	if err := res.Decode(&resp); err != nil {
		return errors.Wrap(err, "failed to decode response of replSetStepDown")
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

// UpdateUserPass updates user's password
func UpdateUserPass(ctx context.Context, client *mongo.Client, name, pass string) error {
	return client.Database("admin").RunCommand(ctx, bson.D{{Key: "updateUser", Value: name}, {Key: "pwd", Value: pass}}).Err()
}

// UpdateUser recreates user with new name and password
// should be used only when username was changed
func UpdateUser(ctx context.Context, client *mongo.Client, currName, newName, pass string) error {
	mu := struct {
		Users []struct {
			Roles interface{} `bson:"roles"`
		} `bson:"users"`
	}{}

	res := client.Database("admin").RunCommand(ctx, bson.D{{Key: "usersInfo", Value: currName}})
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "get user")
	}
	err := res.Decode(&mu)
	if err != nil {
		return errors.Wrap(err, "decode user")
	}

	if len(mu.Users) == 0 {
		return errors.New("empty user data")
	}

	err = client.Database("admin").RunCommand(context.TODO(), bson.D{{Key: "createUser", Value: newName}, {Key: "pwd", Value: pass}, {Key: "roles", Value: mu.Users[0].Roles}}).Err()
	if err != nil {
		return errors.Wrap(err, "create user")
	}

	err = client.Database("admin").RunCommand(context.TODO(), bson.D{{Key: "dropUser", Value: currName}}).Err()
	return errors.Wrap(err, "drop user")
}

// RemoveOld removes from the list those members which are not present in the given list.
// It always should leave at least one element. The config won't be valid for mongo otherwise.
// Better, if the last element has the smallest ID in order not to produce defragmentation
// when the next element will be added (ID = maxID + 1). Mongo replica set member ID must be between 0 and 255, so it matters.
func (m *ConfigMembers) RemoveOld(compareWith ConfigMembers) (changes bool) {
	cm := make(map[string]struct{}, len(compareWith))

	for _, member := range compareWith {
		cm[member.Host] = struct{}{}
	}

	// going from the end to the starting in order to leave last element with the smallest id
	for i := len(*m) - 1; i >= 0 && len(*m) > 1; i-- {
		member := []ConfigMember(*m)[i]
		if _, ok := cm[member.Host]; !ok {
			*m = append([]ConfigMember(*m)[:i], []ConfigMember(*m)[i+1:]...)
			changes = true
		}
	}

	return changes
}

// AddNew adds new members from given list
func (m *ConfigMembers) AddNew(from ConfigMembers) (changes bool) {
	cm := make(map[string]struct{}, len(*m))
	lastID := 0

	for _, member := range *m {
		cm[member.Host] = struct{}{}
		if member.ID > lastID {
			lastID = member.ID
		}
	}

	for _, member := range from {
		if _, ok := cm[member.Host]; !ok {
			lastID++
			member.ID = lastID
			*m = append(*m, member)
			changes = true
		}
	}

	return changes
}

// SetVotes sets voting parameters for members list
func (m *ConfigMembers) SetVotes() {
	votes := 0
	lastVoteIdx := -1
	for i, member := range *m {
		if member.Hidden {
			continue
		}

		if _, ok := member.Tags["nonVoting"]; ok {
			// Non voting member is a regular ReplSet member with
			// votes and priority equals to 0.

			[]ConfigMember(*m)[i].Votes = 0
			[]ConfigMember(*m)[i].Priority = 0

			continue
		}

		if votes < MaxVotingMembers {
			[]ConfigMember(*m)[i].Votes = 1
			votes++
			if !member.ArbiterOnly {
				lastVoteIdx = i
				[]ConfigMember(*m)[i].Priority = 1
			}
		} else if member.ArbiterOnly {
			// Arbiter should always have a vote
			[]ConfigMember(*m)[i].Votes = 1

			// We're over the max voters limit. Make room for the arbiter
			[]ConfigMember(*m)[lastVoteIdx].Votes = 0
			[]ConfigMember(*m)[lastVoteIdx].Priority = 0
		}
	}

	if votes == 0 {
		return
	}

	if votes%2 == 0 {
		[]ConfigMember(*m)[lastVoteIdx].Votes = 0
		[]ConfigMember(*m)[lastVoteIdx].Priority = 0
	}
}

func (m ConfigMember) String() string {
	return fmt.Sprintf("{votes: %d, priority: %d}", m.Votes, m.Priority)
}
