package mongo

import (
	"bytes"
	"context"

	"fmt"
	"strings"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

const dbAdmin = "admin"

type mongoClientExec struct {
	pod *corev1.Pod
	// rs       *psmdbv1.ReplsetSpec
	cli      *clientcmd.Client
	username string
	pass     string
}

func NewMongoClientExec(cl client.Client, rs *psmdbv1.ReplsetSpec, cr *psmdbv1.PerconaServerMongoDB, username, pass string) (Client, error) {
	pod := &corev1.Pod{}

	nn := types.NamespacedName{Namespace: cr.Namespace, Name: rs.PodName(cr, 0)}
	if err := cl.Get(context.TODO(), nn, pod); err != nil {
		return nil, err
	}

	cli, err := clientcmd.NewClient()
	if err != nil {
		return nil, err
	}

	return &mongoClientExec{
		pod: pod,
		cli: cli,
		// rs:       rs,
		username: username,
		pass:     pass,
	}, nil
}

func NewMongosClientExec(cl client.Client, cr *psmdbv1.PerconaServerMongoDB, username, pass string) (Client, error) {
	pod := &corev1.Pod{}

	nn := types.NamespacedName{Namespace: cr.Namespace, Name: fmt.Sprintf("%s-mongos-%d", cr.Name, 0)}
	if err := cl.Get(context.TODO(), nn, pod); err != nil {
		return nil, err
	}

	cli, err := clientcmd.NewClient()
	if err != nil {
		return nil, err
	}

	return &mongoClientExec{
		pod:      pod,
		cli:      cli,
		username: username,
		pass:     pass,
	}, nil
}

func NewStandaloneClientExec(cl client.Client, cr *psmdbv1.PerconaServerMongoDB, host, username, pass string) (Client, error) {
	podName := strings.Split(host, ".")[0]
	if podName == "" {
		return nil, errors.Errorf("invalid host: %s", host)
	}

	pod := &corev1.Pod{}

	nn := types.NamespacedName{Namespace: cr.Namespace, Name: podName}
	if err := cl.Get(context.TODO(), nn, pod); err != nil {
		return nil, err
	}

	cli, err := clientcmd.NewClient()
	if err != nil {
		return nil, err
	}

	return &mongoClientExec{
		pod: pod,
		cli: cli,
		// rs:       rs,
		username: username,
		pass:     pass,
	}, nil
}

func (m *mongoClientExec) exec(ctx context.Context, db, fragment string, outb, errb *bytes.Buffer) error {
	cmd := []string{"mongosh", fmt.Sprintf("mongodb://localhost/%s", db),
		"--quiet", "-p", m.pass, "-u", m.username, "--eval", fmt.Sprintf("EJSON.stringify(%s)", fragment)}

	err := m.cli.Exec(ctx, m.pod, "mongod", cmd, nil, outb, errb, false)
	if err != nil {
		// sout := sensitiveRegexp.ReplaceAllString(outb.String(), ":*****@")
		// serr := sensitiveRegexp.ReplaceAllString(errb.String(), ":*****@")
		return errors.Wrapf(err, "run %s, stdout: %s, stderr: %s", cmd, outb.String(), errb.String())
	}

	if strings.Contains(errb.String(), "MongoServerError:") {
		// serr := sensitiveRegexp.ReplaceAllString(errb.String(), ":*****@")
		return fmt.Errorf("mongo error: %s", errb.String())
	}

	return nil
}

func runCmd[T any](ctx context.Context, cli *mongoClientExec, db, fragment string) (T, error) {
	var outb, errb bytes.Buffer

	var res T
	err := cli.exec(ctx, db, fragment, &outb, &errb)
	if err != nil {
		return res, err
	}

	bson.UnmarshalExtJSON(outb.Bytes(), false, &res)

	return res, nil
}

func (c *mongoClientExec) Disconnect(ctx context.Context) error {
	return nil
}

func (c *mongoClientExec) Ping(ctx context.Context, rp *readpref.ReadPref) error {
	return nil
}

func (c *mongoClientExec) Database(name string, opts ...*options.DatabaseOptions) ClientDatabase {
	return nil
}

// TODO: implement
func (client *mongoClientExec) SetDefaultRWConcern(ctx context.Context, readConcern, writeConcern string) error {
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

// TODO: implement
func (client *mongoClientExec) ReadConfig(ctx context.Context) (RSConfig, error) {
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

// TODO: implement
func (client *mongoClientExec) CreateRole(ctx context.Context, role string, privileges []RolePrivilege, roles []interface{}) error {
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

	res := client.Database("admin").RunCommand(ctx, m)
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

// TODO: implement
func (client *mongoClientExec) UpdateRole(ctx context.Context, role string, privileges []RolePrivilege, roles []interface{}) error {
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
		{Key: "updateRole", Value: role},
		{Key: "privileges", Value: privilegesArr},
		{Key: "roles", Value: rolesArr},
	}

	res := client.Database("admin").RunCommand(ctx, m)
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

// TODO: implement
func (client *mongoClientExec) GetRole(ctx context.Context, role string) (*Role, error) {
	resp := RoleInfo{}

	res := client.Database("admin").RunCommand(ctx, bson.D{
		{Key: "rolesInfo", Value: role},
		{Key: "showPrivileges", Value: true},
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
	return &resp.Roles[0], nil
}

// TODO: implement
func (client *mongoClientExec) CreateUser(ctx context.Context, user, pwd string, roles ...map[string]interface{}) error {
	resp := OKResponse{}

	res := client.Database("admin").RunCommand(ctx, bson.D{
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

// TODO: implement
func (client *mongoClientExec) AddShard(ctx context.Context, rsName, host string) error {
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

// TODO: implement
func (client *mongoClientExec) WriteConfig(ctx context.Context, cfg RSConfig) error {
	log := logf.FromContext(ctx)
	resp := OKResponse{}

	log.V(1).Info("Running replSetReconfig config", "cfg", cfg)

	res := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetReconfig", Value: cfg}})
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

func (client *mongoClientExec) RSStatus(ctx context.Context) (Status, error) {
	var status Status

	// var outb, errb bytes.Buffer
	// client.exec(ctx, dbAdmin, "EJSON.stringify(db.adminCommand({replSetGetStatus: 1}))", &outb, &errb)

	// bson.UnmarshalExtJSON(outb.Bytes(), false, &status)

	status, err := runCmd[Status](ctx, client, dbAdmin, "db.adminCommand({replSetGetStatus: 1})")
	if err != nil {
		return status, err
	}

	if status.OK != 1 {
		return status, errors.Errorf("mongo says: %s", status.Errmsg)
	}

	return status, nil
}

// TODO: implement
func (client *mongoClientExec) StartBalancer(ctx context.Context) error {
	// return switchBalancer(ctx, client, "balancerStart")
	return nil
}

// TODO: implement
func (client *mongoClientExec) StopBalancer(ctx context.Context) error {
	// return switchBalancer(ctx, client, "balancerStop")
	return nil
}

// TODO: implement
func (client *mongoClientExec) IsBalancerRunning(ctx context.Context) (bool, error) {
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

// TODO: implement
func (client *mongoClientExec) GetFCV(ctx context.Context) (string, error) {
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

// TODO: implement
func (client *mongoClientExec) SetFCV(ctx context.Context, version string) error {
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

// TODO: implement
func (client *mongoClientExec) ListDBs(ctx context.Context) (DBList, error) {
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

// TODO: implement
func (client *mongoClientExec) ListShard(ctx context.Context) (ShardList, error) {
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

// TODO: implement
func (client *mongoClientExec) RemoveShard(ctx context.Context, shard string) (ShardRemoveResp, error) {
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

// TODO: implement
func (client *mongoClientExec) RSBuildInfo(ctx context.Context) (BuildInfo, error) {
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

// TODO: implement
func (client *mongoClientExec) StepDown(ctx context.Context, force bool) error {
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

// TODO: implement
func (client *mongoClientExec) IsMaster(ctx context.Context) (*IsMasterResp, error) {
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

// TODO: implement
func (client *mongoClientExec) GetUserInfo(ctx context.Context, username string) (*User, error) {
	resp := UsersInfo{}
	res := client.Database("admin").RunCommand(ctx, bson.D{{Key: "usersInfo", Value: username}})
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

// TODO: implement
func (client *mongoClientExec) UpdateUserRoles(ctx context.Context, username string, roles []map[string]interface{}) error {
	return client.Database("admin").RunCommand(ctx, bson.D{{Key: "updateUser", Value: username}, {Key: "roles", Value: roles}}).Err()
}

// TODO: implement
// UpdateUserPass updates user's password
func (client *mongoClientExec) UpdateUserPass(ctx context.Context, name, pass string) error {
	return client.Database("admin").RunCommand(ctx, bson.D{{Key: "updateUser", Value: name}, {Key: "pwd", Value: pass}}).Err()
}

// TODO: implement
// UpdateUser recreates user with new name and password
// should be used only when username was changed
func (client *mongoClientExec) UpdateUser(ctx context.Context, currName, newName, pass string) error {
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
