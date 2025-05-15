package mongo

import (
	"context"
	"crypto/tls"
	"fmt"
	"reflect"
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

type Client interface {
	Disconnect(ctx context.Context) error
	Database(name string, opts ...*options.DatabaseOptions) ClientDatabase
	Ping(ctx context.Context, rp *readpref.ReadPref) error

	SetDefaultRWConcern(ctx context.Context, readConcern, writeConcern string) error
	ReadConfig(ctx context.Context) (RSConfig, error)
	CreateRole(ctx context.Context, db string, role Role) error
	UpdateRole(ctx context.Context, db string, role Role) error
	GetRole(ctx context.Context, db, role string) (*Role, error)
	CreateUser(ctx context.Context, db, user, pwd string, roles ...Role) error
	AddShard(ctx context.Context, rsName, host string) error
	WriteConfig(ctx context.Context, cfg RSConfig, force bool) error
	RSStatus(ctx context.Context) (Status, error)
	StartBalancer(ctx context.Context) error
	StopBalancer(ctx context.Context) error
	IsBalancerRunning(ctx context.Context) (bool, error)
	GetFCV(ctx context.Context) (string, error)
	SetFCV(ctx context.Context, version string) error
	ListDBs(ctx context.Context) (DBList, error)
	ListShard(ctx context.Context) (ShardList, error)
	RemoveShard(ctx context.Context, shard string) (ShardRemoveResp, error)
	RSBuildInfo(ctx context.Context) (BuildInfo, error)
	StepDown(ctx context.Context, seconds int, force bool) error
	Freeze(ctx context.Context, seconds int) error
	IsMaster(ctx context.Context) (*IsMasterResp, error)
	GetUserInfo(ctx context.Context, username, db string) (*User, error)
	UpdateUserRoles(ctx context.Context, db, username string, roles []Role) error
	UpdateUserPass(ctx context.Context, db, name, pass string) error
	UpdateUser(ctx context.Context, currName, newName, pass string) error
}

type ClientDatabase interface {
	RunCommand(ctx context.Context, runCommand interface{}, opts ...*options.RunCmdOptions) *mongo.SingleResult
}

type mongoClient struct {
	*mongo.Client
}

func (c *mongoClient) Database(name string, opts ...*options.DatabaseOptions) ClientDatabase {
	return c.Client.Database(name, opts...)
}

func ToInterface(client *mongo.Client) Client {
	return &mongoClient{client}
}

func Dial(conf *Config) (Client, error) {
	ctx, connectcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer connectcancel()

	journal := true
	wc := writeconcern.Majority()
	wc.Journal = &journal
	opts := options.Client().
		SetHosts(conf.Hosts).
		SetWriteConcern(wc).
		SetReadPreference(readpref.Primary()).
		SetTLSConfig(conf.TLSConf).
		SetDirect(conf.Direct).
		SetConnectTimeout(10 * time.Second).
		SetServerSelectionTimeout(10 * time.Second)

	if conf.ReplSetName != "" {
		opts.SetReplicaSet(conf.ReplSetName)
	}
	if conf.Username != "" || conf.Password != "" {
		opts.SetAuth(options.Credential{
			Password: conf.Password,
			Username: conf.Username,
		})
	}

	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, errors.Wrap(err, "connect to mongo rs")
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
		return nil, errors.Wrap(err, "ping mongo")
	}

	return ToInterface(client), nil
}

func (client *mongoClient) SetDefaultRWConcern(ctx context.Context, readConcern, writeConcern string) error {
	cmd := bson.D{
		{Key: "setDefaultRWConcern", Value: 1},
		{Key: "defaultReadConcern", Value: bson.D{{Key: "level", Value: readConcern}}},
		{Key: "defaultWriteConcern", Value: bson.D{{Key: "w", Value: writeConcern}}},
	}

	res := client.Database("admin").RunCommand(ctx, cmd)
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "setDefaultRWConcern")
	}

	return nil
}

func (client *mongoClient) ReadConfig(ctx context.Context) (RSConfig, error) {
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

func (client *mongoClient) CreateRole(ctx context.Context, db string, role Role) error {
	resp := OKResponse{}

	privilegesArr := bson.A{}
	for _, p := range role.Privileges {
		privilegesArr = append(privilegesArr, p)
	}

	rolesArr := bson.A{}
	for _, r := range role.Roles {
		rolesArr = append(rolesArr, r)
	}

	authRestrictionsArr := bson.A{}
	for _, r := range role.AuthenticationRestrictions {
		m := bson.M{}

		if len(r.ServerAddress) > 0 {
			m["serverAddress"] = r.ServerAddress
		}

		if len(r.ClientSource) > 0 {
			m["clientSource"] = r.ClientSource
		}

		authRestrictionsArr = append(authRestrictionsArr, m)
	}

	m := bson.D{
		{Key: "createRole", Value: role.Role},
		{Key: "privileges", Value: privilegesArr},
		{Key: "roles", Value: rolesArr},
		{Key: "authenticationRestrictions", Value: authRestrictionsArr},
	}

	res := client.Database(db).RunCommand(ctx, m)
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

func (client *mongoClient) UpdateRole(ctx context.Context, db string, role Role) error {
	resp := OKResponse{}

	privilegesArr := bson.A{}
	for _, p := range role.Privileges {
		privilegesArr = append(privilegesArr, p)
	}

	rolesArr := bson.A{}
	for _, r := range role.Roles {
		rolesArr = append(rolesArr, r)
	}

	authRestrictionsArr := bson.A{}
	for _, r := range role.AuthenticationRestrictions {
		m := bson.M{}

		if len(r.ServerAddress) > 0 {
			m["serverAddress"] = r.ServerAddress
		}

		if len(r.ClientSource) > 0 {
			m["clientSource"] = r.ClientSource
		}

		authRestrictionsArr = append(authRestrictionsArr, m)
	}

	m := bson.D{
		{Key: "updateRole", Value: role.Role},
		{Key: "privileges", Value: privilegesArr},
		{Key: "roles", Value: rolesArr},
		{Key: "authenticationRestrictions", Value: authRestrictionsArr},
	}

	res := client.Database(db).RunCommand(ctx, m)
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

func (client *mongoClient) GetRole(ctx context.Context, db, role string) (*Role, error) {
	resp := RoleInfo{}

	res := client.Database(db).RunCommand(ctx, bson.D{
		{Key: "rolesInfo", Value: role},
		{Key: "showPrivileges", Value: true},
		{Key: "showAuthenticationRestrictions", Value: true},
	})
	if res.Err() != nil {
		return nil, errors.Wrap(res.Err(), "run command")
	}

	err := res.Decode(&resp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode response")
	}
	if resp.OK != 1 {
		return nil, errors.Errorf("mongo says: %s", resp.Errmsg)
	}
	if len(resp.Roles) == 0 {
		return nil, nil
	}

	r := &Role{
		Role:       role,
		DB:         resp.Roles[0].DB,
		Roles:      resp.Roles[0].Roles,
		Privileges: resp.Roles[0].Privileges,
	}

	if len(resp.Roles[0].AuthenticationRestrictions) > 0 {
		r.AuthenticationRestrictions = resp.Roles[0].AuthenticationRestrictions[0]
	}

	return r, nil
}

func (client *mongoClient) CreateUser(ctx context.Context, db, user, pwd string, roles ...Role) error {
	resp := OKResponse{}

	d := bson.D{
		{Key: "createUser", Value: user},
		{Key: "roles", Value: roles},
	}

	if db != "$external" {
		d = append(d, bson.E{Key: "pwd", Value: pwd})
	}

	res := client.Database(db).RunCommand(ctx, d)
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

func (client *mongoClient) AddShard(ctx context.Context, rsName, host string) error {
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

func (client *mongoClient) WriteConfig(ctx context.Context, cfg RSConfig, force bool) error {
	log := logf.FromContext(ctx)
	resp := OKResponse{}

	log.V(1).Info("Running replSetReconfig config", "cfg", cfg)

	res := client.Database("admin").RunCommand(ctx, bson.D{
		{Key: "replSetReconfig", Value: cfg},
		{Key: "force", Value: force},
	})
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

func (client *mongoClient) RSStatus(ctx context.Context) (Status, error) {
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

func (client *mongoClient) StartBalancer(ctx context.Context) error {
	return switchBalancer(ctx, client, "balancerStart")
}

func (client *mongoClient) StopBalancer(ctx context.Context) error {
	return switchBalancer(ctx, client, "balancerStop")
}

func switchBalancer(ctx context.Context, client *mongoClient, command string) error {
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

func (client *mongoClient) IsBalancerRunning(ctx context.Context) (bool, error) {
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

func (client *mongoClient) GetFCV(ctx context.Context) (string, error) {
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

func (client *mongoClient) SetFCV(ctx context.Context, version string) error {
	res := OKResponse{}
	command := "setFeatureCompatibilityVersion"

	var resp *mongo.SingleResult
	if version == "4.4" || version == "5.0" || version == "6.0" {
		resp = client.Database("admin").RunCommand(ctx, bson.D{{Key: command, Value: version}})
	} else {
		resp = client.Database("admin").RunCommand(ctx, bson.D{{Key: command, Value: version}, {Key: "confirm", Value: true}})
	}
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

func (client *mongoClient) ListDBs(ctx context.Context) (DBList, error) {
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

func (client *mongoClient) ListShard(ctx context.Context) (ShardList, error) {
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

func (client *mongoClient) RemoveShard(ctx context.Context, shard string) (ShardRemoveResp, error) {
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

func (client *mongoClient) RSBuildInfo(ctx context.Context) (BuildInfo, error) {
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

func (client *mongoClient) StepDown(ctx context.Context, seconds int, force bool) error {
	resp := OKResponse{}

	res := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetStepDown", Value: seconds}, {Key: "force", Value: force}})
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

func (client *mongoClient) Freeze(ctx context.Context, seconds int) error {
	resp := OKResponse{}

	res := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetFreeze", Value: seconds}})
	err := res.Err()
	if err != nil {
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

func (client *mongoClient) IsMaster(ctx context.Context) (*IsMasterResp, error) {
	cur := client.Database("admin").RunCommand(ctx, bson.D{{Key: "isMaster", Value: 1}})
	if cur.Err() != nil {
		return nil, errors.Wrap(cur.Err(), "run isMaster")
	}

	resp := IsMasterResp{}
	if err := cur.Decode(&resp); err != nil {
		return nil, errors.Wrap(err, "decode isMaster response")
	}

	if resp.OK != 1 {
		return nil, errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return &resp, nil
}

func (client *mongoClient) GetUserInfo(ctx context.Context, username, db string) (*User, error) {
	resp := UsersInfo{}
	res := client.Database(db).RunCommand(ctx, bson.D{{Key: "usersInfo", Value: username}})
	if res.Err() != nil {
		return nil, errors.Wrap(res.Err(), "run command")
	}

	err := res.Decode(&resp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decode response")
	}
	if resp.OK != 1 {
		return nil, errors.Errorf("mongo says: %s", resp.Errmsg)
	}
	if len(resp.Users) == 0 {
		return nil, nil
	}
	return &resp.Users[0], nil
}

func (client *mongoClient) UpdateUserRoles(ctx context.Context, db, username string, roles []Role) error {
	return client.Database(db).RunCommand(ctx, bson.D{{Key: "updateUser", Value: username}, {Key: "roles", Value: roles}}).Err()
}

// UpdateUserPass updates user's password
func (client *mongoClient) UpdateUserPass(ctx context.Context, db, name, pass string) error {
	return client.Database(db).RunCommand(ctx, bson.D{{Key: "updateUser", Value: name}, {Key: "pwd", Value: pass}}).Err()
}

// UpdateUser recreates user with new name and password
// should be used only when username was changed
func (client *mongoClient) UpdateUser(ctx context.Context, currName, newName, pass string) error {
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

	err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "createUser", Value: newName}, {Key: "pwd", Value: pass}, {Key: "roles", Value: mu.Users[0].Roles}}).Err()
	if err != nil {
		return errors.Wrap(err, "create user")
	}

	err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "dropUser", Value: currName}}).Err()
	return errors.Wrap(err, "drop user")
}

// RemoveOld removes from the list those members which are not present in the given list.
// It always should leave at least one element. The config won't be valid for mongo otherwise.
// Better, if the last element has the smallest ID in order not to produce defragmentation
// when the next element will be added (ID = maxID + 1). Mongo replica set member ID must be between 0 and 255, so it matters.
func (m *ConfigMembers) RemoveOld(ctx context.Context, compareWith ConfigMembers) bool {
	log := logf.FromContext(ctx)

	cm := make(map[string]struct{}, len(compareWith))

	for _, member := range compareWith {
		cm[member.Host] = struct{}{}
	}

	// going from the end to the starting in order to leave last element with the smallest id
	for i := len(*m) - 1; i >= 0 && len(*m) > 1; i-- {
		member := []ConfigMember(*m)[i]
		if _, ok := cm[member.Host]; !ok {
			*m = append([]ConfigMember(*m)[:i], []ConfigMember(*m)[i+1:]...)
			log.Info("Removing old member from replset", "_id", member.ID, "host", member.Host)
			return true
		}
	}

	return false
}

func (m *ConfigMembers) FixMemberHostnames(ctx context.Context, compareWith ConfigMembers, rsStatus Status) (member *Member, changes bool) {
	log := logf.FromContext(ctx)

	if len(*m) < 1 {
		return member, changes
	}

	type configMember struct {
		Host string `bson:"host" json:"host"`
	}

	cm := make(map[string]configMember, len(compareWith))

	for _, mem := range compareWith {
		name, ok := mem.Tags["podName"]
		if !ok {
			continue
		}
		cm[name] = configMember{Host: mem.Host}
	}

	// Update secondaries first
	for _, sMem := range rsStatus.Members {
		if sMem.State == MemberStatePrimary {
			continue
		}

		for i := 0; i < len(*m); i++ {
			mem := []ConfigMember(*m)[i]
			if sMem.Id != mem.ID {
				continue
			}
			podName, ok := mem.Tags["podName"]
			if !ok {
				continue
			}
			c, ok := cm[podName]
			if ok && c.Host != mem.Host {
				log.Info(
					"Host changed",
					"pod", podName,
					"state", MemberStateStrings[sMem.State],
					"old", mem.Host,
					"new", c.Host,
				)
				[]ConfigMember(*m)[i].Host = c.Host
				return sMem, true
			}
		}
	}

	// Update primary last
	for _, sMem := range rsStatus.Members {
		if sMem.State == MemberStateSecondary {
			continue
		}

		for i := 0; i < len(*m); i++ {
			mem := []ConfigMember(*m)[i]
			if sMem.Id != mem.ID {
				continue
			}
			podName, ok := mem.Tags["podName"]
			if !ok {
				continue
			}
			c, ok := cm[podName]
			if ok && c.Host != mem.Host {
				log.Info(
					"Host changed",
					"pod", podName,
					"state", MemberStateStrings[sMem.State],
					"old", mem.Host,
					"new", c.Host,
				)
				[]ConfigMember(*m)[i].Host = c.Host
				return sMem, true
			}
		}
	}

	return member, false
}

func (m *ConfigMembers) FixMemberConfigs(ctx context.Context, compareWith ConfigMembers) (changes bool) {
	log := logf.FromContext(ctx)

	if len(*m) < 1 {
		return changes
	}

	type configMember struct {
		Tags     ReplsetTags       `bson:"tags,omitempty" json:"tags,omitempty"`
		Horizons map[string]string `bson:"horizons,omitempty" json:"horizons,omitempty"`
	}

	cm := make(map[string]configMember, len(compareWith))

	for _, member := range compareWith {
		cm[member.Host] = configMember{
			Tags:     member.Tags,
			Horizons: member.Horizons,
		}
	}

	for i := 0; i < len(*m); i++ {
		member := []ConfigMember(*m)[i]
		c, ok := cm[member.Host]
		if ok && c.Tags != nil && !reflect.DeepEqual(c.Tags, member.Tags) {
			changes = true
			[]ConfigMember(*m)[i].Tags = c.Tags
			log.Info("Tags changed", "host", member.Host, "old", member.Tags, "new", c.Tags)
		}
		if ok && !reflect.DeepEqual(c.Horizons, member.Horizons) {
			changes = true
			[]ConfigMember(*m)[i].Horizons = c.Horizons
			log.Info("Horizons changed", "host", member.Host, "old", member.Horizons, "new", c.Horizons)
		}
	}

	return changes
}

// ExternalNodesChanged checks if votes or priority fields changed for external nodes
func (m *ConfigMembers) ExternalNodesChanged(compareWith ConfigMembers) bool {
	cm := make(map[string]struct {
		votes    int
		priority int
		tags     ReplsetTags
	}, len(compareWith))

	for _, member := range compareWith {
		_, ok := member.Tags["external"]
		if !ok {
			continue
		}
		cm[member.Host] = struct {
			votes    int
			priority int
			tags     ReplsetTags
		}{
			votes:    member.Votes,
			priority: member.Priority,
			tags:     member.Tags,
		}
	}

	for i := 0; i < len(*m); i++ {
		member := []ConfigMember(*m)[i]
		if ext, ok := cm[member.Host]; ok {
			if ext.votes != member.Votes ||
				ext.priority != member.Priority ||
				!reflect.DeepEqual(ext.tags, member.Tags) {

				[]ConfigMember(*m)[i].Votes = ext.votes
				[]ConfigMember(*m)[i].Priority = ext.priority
				[]ConfigMember(*m)[i].Tags = ext.tags

				return true
			}
		}
	}

	return false
}

// AddNew adds a new member from given list to the config.
// It adds only one at a time. Returns true if it adds any member.
func (m *ConfigMembers) AddNew(ctx context.Context, from ConfigMembers) bool {
	log := logf.FromContext(ctx)

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
			log.Info("Adding new member to replset", "_id", member.ID, "host", member.Host)
			return true
		}
	}

	return false
}

// SetVotes sets voting parameters for members list
func (m *ConfigMembers) SetVotes(compareWith ConfigMembers, unsafePSA bool) {
	votes := 0
	lastVoteIdx := -1

	cm := make(map[string]int, len(compareWith))

	for _, member := range compareWith {
		if member.ArbiterOnly {
			continue
		}

		cm[member.Host] = member.Priority
	}

	for i, member := range *m {
		if _, ok := member.Tags["external"]; ok {
			[]ConfigMember(*m)[i].Votes = member.Votes
			[]ConfigMember(*m)[i].Priority = member.Priority

			if member.Votes == 1 {
				votes++
			}

			continue
		}

		if _, ok := member.Tags["nonVoting"]; ok {
			// Non voting member is a regular ReplSet member with
			// votes and priority equals to 0.

			[]ConfigMember(*m)[i].Votes = 0
			[]ConfigMember(*m)[i].Priority = 0

			continue
		}

		if _, ok := member.Tags["hidden"]; ok {
			// Hidden member is a voting ReplSet member
			// but it is not listed in hello command.

			[]ConfigMember(*m)[i].Votes = 1
			[]ConfigMember(*m)[i].Priority = 0
			votes++

			continue
		}

		if member.ArbiterOnly {
			// Arbiter should always have a vote
			[]ConfigMember(*m)[i].Votes = DefaultVotes
			// Arbiter should never have priority
			[]ConfigMember(*m)[i].Priority = 0
		} else {
			[]ConfigMember(*m)[i].Votes = DefaultVotes
			lastVoteIdx = i

			// In unsafe PSA (Primary with a Secondary and an Arbiter),
			// we are unable to set the votes and the priority simultaneously.
			// Therefore, setting only the priority.
			if !unsafePSA || member.Votes == DefaultVotes {
				// Priority can be any number in range [0,1000].
				// We're setting it to 2 as default, to allow
				// users to configure external nodes with lower
				// priority than local nodes.
				priority := DefaultPriority

				if c, ok := cm[member.Host]; ok {
					priority = c
				}

				[]ConfigMember(*m)[i].Priority = priority
			}
		}
		votes++

		if votes > MaxVotingMembers {
			if member.ArbiterOnly {
				[]ConfigMember(*m)[lastVoteIdx].Votes = 0
				[]ConfigMember(*m)[lastVoteIdx].Priority = 0
			} else {
				[]ConfigMember(*m)[i].Votes = 0
				[]ConfigMember(*m)[i].Priority = 0
			}
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
