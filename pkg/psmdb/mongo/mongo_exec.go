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
	pod      *corev1.Pod
	cli      *clientcmd.Client
	username string
	pass     string
}

func NewMongoClientExec(ctx context.Context, cl client.Client, rs *psmdbv1.ReplsetSpec, cr *psmdbv1.PerconaServerMongoDB, username, pass string) (Client, error) {
	pod := &corev1.Pod{}

	nn := types.NamespacedName{Namespace: cr.Namespace, Name: rs.PodName(cr, 0)}
	if err := cl.Get(ctx, nn, pod); err != nil {
		return nil, err
	}

	// TODO: Check and make sure that pod is the replset primary

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

func NewMongosClientExec(ctx context.Context, cl client.Client, cr *psmdbv1.PerconaServerMongoDB, username, pass string) (Client, error) {
	pod := &corev1.Pod{}

	nn := types.NamespacedName{Namespace: cr.Namespace, Name: fmt.Sprintf("%s-mongos-%d", cr.Name, 0)}
	if err := cl.Get(ctx, nn, pod); err != nil {
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

func NewStandaloneClientExec(ctx context.Context, cl client.Client, cr *psmdbv1.PerconaServerMongoDB, host, username, pass string) (Client, error) {
	podName := strings.Split(host, ".")[0]
	if podName == "" {
		return nil, errors.Errorf("invalid host: %s", host)
	}

	pod := &corev1.Pod{}

	nn := types.NamespacedName{Namespace: cr.Namespace, Name: podName}
	if err := cl.Get(ctx, nn, pod); err != nil {
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

func (client *mongoClientExec) SetDefaultRWConcern(ctx context.Context, readConcern, writeConcern string) error {
	b := bson.D{
		{Key: "setDefaultRWConcern", Value: 1},
		{Key: "defaultReadConcern", Value: bson.D{{Key: "level", Value: readConcern}}},
		{Key: "defaultWriteConcern", Value: bson.D{{Key: "w", Value: writeConcern}}},
	}

	jsonCmd, err := bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return err
	}

	resp := OKResponse{}
	resp, err = runCmd[OKResponse](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		return errors.Wrap(err, "setDefaultRWConcern")
	}
	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func (client *mongoClientExec) ReadConfig(ctx context.Context) (RSConfig, error) {
	resp, err := runCmd[ReplSetGetConfig](ctx, client, dbAdmin, "db.adminCommand({replSetGetConfig: 1})")

	if err != nil {
		return RSConfig{}, errors.Wrap(err, "replSetGetConfig")
	}

	if resp.OK != 1 || resp.Config == nil {
		return RSConfig{}, errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return *resp.Config, nil
}

func (client *mongoClientExec) CreateRole(ctx context.Context, role string, privileges []RolePrivilege, roles []interface{}) error {
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

	jsonCmd, err := bson.MarshalExtJSON(m, false, false)
	if err != nil {
		return err
	}

	resp, err := runCmd[OKResponse](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		return errors.Wrap(err, "failed to create role")
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func (client *mongoClientExec) UpdateRole(ctx context.Context, role string, privileges []RolePrivilege, roles []interface{}) error {
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

	jsonCmd, err := bson.MarshalExtJSON(m, false, false)
	if err != nil {
		return err
	}

	resp, err := runCmd[OKResponse](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		return errors.Wrap(err, "failed to update role")
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func (client *mongoClientExec) GetRole(ctx context.Context, role string) (*Role, error) {
	b := bson.D{
		{Key: "rolesInfo", Value: role},
		{Key: "showPrivileges", Value: true},
	}
	jsonCmd, err := bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return nil, err
	}

	resp, err := runCmd[RoleInfo](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		return nil, err
	}

	if resp.OK != 1 {
		return nil, errors.Errorf("mongo says: %s", resp.Errmsg)
	}
	if len(resp.Roles) == 0 {
		return nil, nil
	}
	return &resp.Roles[0], nil
}

func (client *mongoClientExec) CreateUser(ctx context.Context, user, pwd string, roles ...map[string]interface{}) error {
	b := bson.D{
		{Key: "createUser", Value: user},
		{Key: "pwd", Value: pwd},
		{Key: "roles", Value: roles},
	}
	jsonCmd, err := bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return err
	}

	resp, err := runCmd[OKResponse](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		return err
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func (client *mongoClientExec) AddShard(ctx context.Context, rsName, host string) error {
	b := bson.D{{Key: "addShard", Value: rsName + "/" + host}}
	jsonCmd, err := bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return err
	}

	resp, err := runCmd[OKResponse](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		return err
	}

	if resp.OK != 1 {
		return errors.Errorf("add shard: %s", resp.Errmsg)
	}

	return nil
}

func (client *mongoClientExec) WriteConfig(ctx context.Context, cfg RSConfig) error {
	log := logf.FromContext(ctx)
	log.V(1).Info("Running replSetReconfig config", "cfg", cfg)

	b := bson.D{{Key: "replSetReconfig", Value: cfg}}
	jsonCmd, err := bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return err
	}

	resp, err := runCmd[OKResponse](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		return err
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func (client *mongoClientExec) RSStatus(ctx context.Context) (Status, error) {
	b := bson.D{
		{Key: "replSetGetStatus", Value: 1},
	}
	jsonCmd, err := bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return Status{}, err
	}

	status, err := runCmd[Status](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		return status, err
	}

	if status.OK != 1 {
		return status, errors.Errorf("mongo says: %s", status.Errmsg)
	}

	return status, nil
}

func (client *mongoClientExec) StartBalancer(ctx context.Context) error {
	resp, err := runCmd[OKResponse](ctx, client, dbAdmin, "db.adminCommand({balancerStart: 1})")
	if err != nil {
		return err
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func (client *mongoClientExec) StopBalancer(ctx context.Context) error {
	resp, err := runCmd[OKResponse](ctx, client, dbAdmin, "db.adminCommand({balancerStop: 1})")
	if err != nil {
		return err
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func (client *mongoClientExec) IsBalancerRunning(ctx context.Context) (bool, error) {
	resp, err := runCmd[BalancerStatus](ctx, client, dbAdmin, "db.adminCommand({balancerStatus: 1})")
	if err != nil {
		return false, err
	}

	if resp.OK != 1 {
		return false, errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return resp.Mode == "full", nil
}

func (client *mongoClientExec) GetFCV(ctx context.Context) (string, error) {
	b := bson.D{
		{Key: "getParameter", Value: 1},
		{Key: "featureCompatibilityVersion", Value: 1},
	}
	jsonCmd, err := bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return "", err
	}

	res, err := runCmd[FCV](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		return "", err
	}

	if res.OK != 1 {
		return "", errors.Errorf("mongo says: %s", res.Errmsg)
	}

	return res.FCV.Version, nil
}

func (client *mongoClientExec) SetFCV(ctx context.Context, version string) error {
	command := "setFeatureCompatibilityVersion"

	b := bson.D{{Key: command, Value: version}}
	jsonCmd, err := bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return err
	}

	res, err := runCmd[OKResponse](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		return err
	}

	if res.OK != 1 {
		return errors.Errorf("mongo says: %s", res.Errmsg)
	}

	return nil
}

func (client *mongoClientExec) ListDBs(ctx context.Context) (DBList, error) {
	dbList, err := runCmd[DBList](ctx, client, dbAdmin, "db.adminCommand({listDatabases: 1})")
	if err != nil {
		return DBList{}, err
	}

	if dbList.OK != 1 {
		return dbList, errors.Errorf("mongo says: %s", dbList.Errmsg)
	}

	return dbList, nil
}

func (client *mongoClientExec) ListShard(ctx context.Context) (ShardList, error) {
	shardList, err := runCmd[ShardList](ctx, client, dbAdmin, "db.adminCommand({listShards: 1})")
	if err != nil {
		return ShardList{}, err
	}

	if shardList.OK != 1 {
		return shardList, errors.Errorf("mongo says: %s", shardList.Errmsg)
	}

	return shardList, nil
}

func (client *mongoClientExec) RemoveShard(ctx context.Context, shard string) (ShardRemoveResp, error) {
	b := bson.D{{Key: "removeShard", Value: shard}}
	jsonCmd, err := bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return ShardRemoveResp{}, err
	}

	removeResp, err := runCmd[ShardRemoveResp](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		return ShardRemoveResp{}, err
	}

	if removeResp.OK != 1 {
		return removeResp, errors.Errorf("mongo says: %s", removeResp.Errmsg)
	}

	return removeResp, nil
}

func (client *mongoClientExec) RSBuildInfo(ctx context.Context) (BuildInfo, error) {
	bi, err := runCmd[BuildInfo](ctx, client, dbAdmin, "db.adminCommand({buildInfo: 1})")
	if err != nil {
		return BuildInfo{}, err
	}

	if bi.OK != 1 {
		return bi, errors.Errorf("mongo says: %s", bi.Errmsg)
	}

	return bi, nil
}

func (client *mongoClientExec) StepDown(ctx context.Context, force bool) error {
	b := bson.D{{Key: "replSetStepDown", Value: 60}, {Key: "force", Value: force}}
	jsonCmd, err := bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return err
	}

	resp, err := runCmd[OKResponse](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		cErr, ok := err.(mongo.CommandError)
		if ok && cErr.HasErrorLabel("NetworkError") {
			// https://docs.mongodb.com/manual/reference/method/rs.stepDown/#client-connections
			return nil
		}
		return errors.Wrap(err, "replSetStepDown")
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func (client *mongoClientExec) IsMaster(ctx context.Context) (*IsMasterResp, error) {
	resp, err := runCmd[IsMasterResp](ctx, client, dbAdmin, "db.adminCommand({isMaster: 1})")
	if err != nil {
		return nil, err
	}

	if resp.OK != 1 {
		return nil, errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return &resp, nil
}

func (client *mongoClientExec) GetUserInfo(ctx context.Context, username string) (*User, error) {
	b := bson.D{{Key: "usersInfo", Value: username}}
	jsonCmd, err := bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return nil, err
	}

	resp, err := runCmd[UsersInfo](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		return nil, err
	}

	if resp.OK != 1 {
		return nil, errors.Errorf("mongo says: %s", resp.Errmsg)
	}
	if len(resp.Users) == 0 {
		return nil, nil
	}
	return &resp.Users[0], nil
}

func (client *mongoClientExec) UpdateUserRoles(ctx context.Context, username string, roles []map[string]interface{}) error {
	return client.Database("admin").RunCommand(ctx, bson.D{{Key: "updateUser", Value: username}, {Key: "roles", Value: roles}}).Err()
}

// UpdateUserPass updates user's password
func (client *mongoClientExec) UpdateUserPass(ctx context.Context, name, pass string) error {
	b := bson.D{{Key: "updateUser", Value: name}, {Key: "pwd", Value: pass}}
	jsonCmd, err := bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return err
	}

	resp, err := runCmd[OKResponse](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		return err
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

// UpdateUser recreates user with new name and password
// should be used only when username was changed
func (client *mongoClientExec) UpdateUser(ctx context.Context, currName, newName, pass string) error {
	type Users struct {
		Users []struct {
			Roles interface{} `bson:"roles"`
		} `bson:"users"`
	}

	b := bson.D{{Key: "usersInfo", Value: currName}}
	jsonCmd, err := bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return err
	}

	mu, err := runCmd[Users](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil {
		return err
	}

	if len(mu.Users) == 0 {
		return errors.New("empty user data")
	}

	b = bson.D{{Key: "createUser", Value: newName}, {Key: "pwd", Value: pass}, {Key: "roles", Value: mu.Users[0].Roles}}
	jsonCmd, err = bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return errors.Wrap(err, "create user")
	}
	resp, err := runCmd[OKResponse](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil || resp.OK != 0 {
		return errors.Wrap(err, "create user")
	}

	b = bson.D{{Key: "dropUser", Value: currName}}
	jsonCmd, err = bson.MarshalExtJSON(b, false, false)
	if err != nil {
		return errors.Wrap(err, "drop user")
	}
	resp, err = runCmd[OKResponse](ctx, client, dbAdmin, fmt.Sprintf("db.adminCommand(%s)", string(jsonCmd)))
	if err != nil || resp.OK != 0 {
		return errors.Wrap(err, "drop user")
	}

	return nil
}
