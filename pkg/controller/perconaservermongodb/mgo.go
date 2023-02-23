package perconaservermongodb

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	mgo "go.mongodb.org/mongo-driver/mongo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

var errReplsetLimit = fmt.Errorf("maximum replset member (%d) count reached", mongo.MaxMembers)

func (r *ReconcilePerconaServerMongoDB) reconcileCluster(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec,
	mongosPods []corev1.Pod) (api.AppState, error) {
	log := logf.FromContext(ctx)

	replsetSize := replset.Size

	if replset.NonVoting.Enabled {
		replsetSize += replset.NonVoting.Size
	}

	if replset.Arbiter.Enabled {
		replsetSize += replset.Arbiter.Size
	}

	restoreInProgress, err := r.restoreInProgress(ctx, cr, replset)
	if err != nil {
		return api.AppStateError, errors.Wrap(err, "check if restore in progress")
	}

	if restoreInProgress {
		return api.AppStateInit, nil
	}

	if replsetSize == 0 {
		return api.AppStateReady, nil
	}

	pods, err := psmdb.GetRSPods(ctx, r.client, cr, replset.Name, false)
	if err != nil {
		return api.AppStateInit, errors.Wrap(err, "failed to get replset pods")
	}

	// all pods needs to be scheduled to reconcile
	if int(replsetSize) > len(pods.Items) {
		log.Info("Waiting for the pods", "replset", replset.Name, "size", replsetSize, "pods", len(pods.Items))
		return api.AppStateInit, nil
	}

	if cr.MCSEnabled() {
		seList, err := psmdb.GetExportedServices(ctx, r.client, cr)
		if err != nil {
			return api.AppStateError, errors.Wrap(err, "get exported services")
		}

		if len(seList.Items) == 0 {
			log.Info("waiting for service exports")
			return api.AppStateInit, nil
		}

		for _, se := range seList.Items {
			imported, err := psmdb.IsServiceImported(ctx, r.client, cr, se.Name)
			if err != nil {
				return api.AppStateError, errors.Wrapf(err, "check if service is imported for %s", se.Name)
			}
			if !imported {
				log.Info("waiting for service import", "replset", replset.Name, "serviceExport", se.Name)
				return api.AppStateInit, nil
			}
		}
	}

	cli, err := r.mongoClientWithRole(ctx, cr, *replset, roleClusterAdmin)
	if err != nil {
		if cr.Spec.Unmanaged {
			return api.AppStateInit, nil
		}
		if cr.Status.Replsets[replset.Name].Initialized {
			return api.AppStateError, errors.Wrap(err, "dial")
		}

		err := r.handleReplsetInit(ctx, cr, replset, pods.Items)
		if err != nil {
			return api.AppStateInit, errors.Wrap(err, "handleReplsetInit")
		}

		err = r.createOrUpdateSystemUsers(ctx, cr, replset)
		if err != nil {
			return api.AppStateInit, errors.Wrap(err, "create system users")
		}

		rs := cr.Status.Replsets[replset.Name]
		rs.Initialized = true
		cr.Status.Replsets[replset.Name] = rs

		cr.Status.AddCondition(api.ClusterCondition{
			Status:             api.ConditionTrue,
			Type:               api.AppStateInit,
			Message:            replset.Name,
			LastTransitionTime: metav1.NewTime(time.Now()),
		})

		return api.AppStateInit, nil
	}

	if cr.Spec.Unmanaged {
		status, err := mongo.RSStatus(ctx, cli)
		if err != nil {
			return api.AppStateError, errors.Wrap(err, "failed to get rs status")
		}
		if status.Primary() == nil {
			return api.AppStateInit, nil
		}
		return api.AppStateReady, nil
	}
	err = r.createOrUpdateSystemUsers(ctx, cr, replset)
	if err != nil {
		return api.AppStateInit, errors.Wrap(err, "create system users")
	}

	defer func() {
		if err := cli.Disconnect(ctx); err != nil {
			log.Error(err, "failed to close connection")
		}
	}()

	// this can happen if cluster is initialized but status update failed
	if !cr.Status.Replsets[replset.Name].Initialized {
		rs := cr.Status.Replsets[replset.Name]
		rs.Initialized = true
		cr.Status.Replsets[replset.Name] = rs

		cr.Status.AddCondition(api.ClusterCondition{
			Status:             api.ConditionTrue,
			Type:               api.AppStateInit,
			Message:            replset.Name,
			LastTransitionTime: metav1.NewTime(time.Now()),
		})

		return api.AppStateInit, nil
	}

	rstRunning, err := r.isRestoreRunning(ctx, cr)
	if err != nil {
		return api.AppStateInit, errors.Wrap(err, "failed to check running restore")
	}

	if cr.Spec.Sharding.Enabled &&
		!rstRunning &&
		cr.Status.Replsets[replset.Name].Initialized &&
		cr.Status.Replsets[replset.Name].Status == api.AppStateReady &&
		cr.Status.Mongos != nil &&
		cr.Status.Mongos.Status == api.AppStateReady &&
		replset.ClusterRole == api.ClusterRoleShardSvr &&
		len(mongosPods) > 0 {

		mongosSession, err := r.mongosClientWithRole(ctx, cr, roleClusterAdmin)
		if err != nil {
			return api.AppStateError, errors.Wrap(err, "failed to get mongos connection")
		}

		defer func() {
			err := mongosSession.Disconnect(ctx)
			if err != nil {
				log.Error(err, "failed to close mongos connection")
			}
		}()

		err = mongo.SetDefaultRWConcern(ctx, mongosSession, mongo.DefaultReadConcern, mongo.DefaultWriteConcern)
		// SetDefaultRWConcern introduced in MongoDB 4.4
		if err != nil && !strings.Contains(err.Error(), "CommandNotFound") {
			return api.AppStateError, errors.Wrap(err, "set default RW concern")
		}

		in, err := inShard(ctx, mongosSession, replset.Name)
		if err != nil {
			return api.AppStateError, errors.Wrap(err, "get shard")
		}

		if !in {
			log.Info("adding rs to shard", "rs", replset.Name)

			err := r.handleRsAddToShard(ctx, cr, replset, pods.Items[0], mongosPods[0])
			if err != nil {
				return api.AppStateError, errors.Wrap(err, "add shard")
			}

			log.Info("added to shard", "rs", replset.Name)
		}

		rs := cr.Status.Replsets[replset.Name]
		t := true
		rs.AddedAsShard = &t
		cr.Status.Replsets[replset.Name] = rs

	}

	if replset.Arbiter.Enabled && !cr.Spec.Sharding.Enabled {
		err := mongo.SetDefaultRWConcern(ctx, cli, mongo.DefaultReadConcern, mongo.DefaultWriteConcern)
		// SetDefaultRWConcern introduced in MongoDB 4.4
		if err != nil && !strings.Contains(err.Error(), "CommandNotFound") {
			return api.AppStateError, errors.Wrap(err, "set default RW concern")
		}
	}

	membersLive, err := r.updateConfigMembers(ctx, cli, cr, replset)
	if err != nil {
		return api.AppStateError, errors.Wrap(err, "failed to update config members")
	}

	if membersLive == len(pods.Items) {
		return api.AppStateReady, nil
	}

	return api.AppStateInit, nil
}

func (r *ReconcilePerconaServerMongoDB) updateConfigMembers(ctx context.Context, cli *mgo.Client, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) (int, error) {
	log := logf.FromContext(ctx)
	// Primary with a Secondary and an Arbiter (PSA)
	unsafePSA := cr.Spec.UnsafeConf && rs.Arbiter.Enabled && rs.Arbiter.Size == 1 && !rs.NonVoting.Enabled && rs.Size == 2

	pods, err := psmdb.GetRSPods(ctx, r.client, cr, rs.Name, false)
	if err != nil {
		return 0, errors.Wrap(err, "get rs pods")
	}

	cnf, err := mongo.ReadConfig(ctx, cli)
	if err != nil {
		return 0, errors.Wrap(err, "get mongo config")
	}

	members := mongo.ConfigMembers{}
	for key, pod := range pods.Items {
		if key >= mongo.MaxMembers {
			log.Error(errReplsetLimit, "rs", rs.Name)
			break
		}

		host, err := psmdb.MongoHost(ctx, r.client, cr, rs.Name, rs.Expose.Enabled, pod)
		if err != nil {
			return 0, fmt.Errorf("get host for pod %s: %v", pod.Name, err)
		}

		member := mongo.ConfigMember{
			ID:           key,
			Host:         host,
			BuildIndexes: true,
			Priority:     mongo.DefaultPriority,
			Votes:        mongo.DefaultVotes,
		}

		switch pod.Labels["app.kubernetes.io/component"] {
		case "arbiter":
			member.ArbiterOnly = true
			member.Priority = 0
		case "mongod", "cfg":
			member.Tags = mongo.ReplsetTags{
				"podName":     pod.Name,
				"serviceName": cr.Name,
			}
		case "nonVoting":
			member.Tags = mongo.ReplsetTags{
				"podName":     pod.Name,
				"serviceName": cr.Name,
				"nonVoting":   "true",
			}
			member.Priority = 0
			member.Votes = 0
		}

		members = append(members, member)
	}

	// sort config members by priority, descending
	sort.Slice(members, func(i, j int) bool {
		return members[i].Priority > members[j].Priority
	})

	memberC := len(members)
	for i, extNode := range rs.ExternalNodes {
		if i+memberC >= mongo.MaxMembers {
			log.Error(errReplsetLimit, "rs", rs.Name)
			break
		}

		member := mongo.ConfigMember{
			ID:           i + memberC,
			Host:         extNode.HostPort(),
			Votes:        extNode.Votes,
			Priority:     extNode.Priority,
			BuildIndexes: true,
			Tags:         mongo.ReplsetTags{"external": "true"},
		}

		members = append(members, member)
	}

	if cnf.Members.FixTags(members) {
		cnf.Version++

		log.Info("Fixing member tags", "replset", rs.Name)

		if err := mongo.WriteConfig(ctx, cli, cnf); err != nil {
			return 0, errors.Wrap(err, "fix tags: write mongo config")
		}
	}

	if cnf.Members.FixHosts(members) {
		cnf.Version++

		log.Info("Fixing member hosts", "replset", rs.Name)

		err = mongo.WriteConfig(ctx, cli, cnf)
		if err != nil {
			return 0, errors.Wrap(err, "fix hosts: write mongo config")
		}
	}

	if cnf.Members.RemoveOld(members) {
		cnf.Version++

		log.Info("Removing old nodes", "replset", rs.Name)

		err = mongo.WriteConfig(ctx, cli, cnf)
		if err != nil {
			return 0, errors.Wrap(err, "delete: write mongo config")
		}
	}

	if cnf.Members.AddNew(members) {
		cnf.Version++

		log.Info("Adding new nodes", "replset", rs.Name)

		err = mongo.WriteConfig(ctx, cli, cnf)
		if err != nil {
			return 0, errors.Wrap(err, "add new: write mongo config")
		}
	}

	if cnf.Members.ExternalNodesChanged(members) {
		cnf.Version++

		log.Info("Updating external nodes", "replset", rs.Name)

		err = mongo.WriteConfig(ctx, cli, cnf)
		if err != nil {
			return 0, errors.Wrap(err, "update external nodes: write mongo config")
		}
	}

	currMembers := append(mongo.ConfigMembers(nil), cnf.Members...)
	cnf.Members.SetVotes(unsafePSA)
	if !reflect.DeepEqual(currMembers, cnf.Members) {
		cnf.Version++

		log.Info("Configuring member votes and priorities", "replset", rs.Name)

		err := mongo.WriteConfig(ctx, cli, cnf)
		if err != nil {
			return 0, errors.Wrap(err, "set votes: write mongo config")
		}
	}

	rsStatus, err := mongo.RSStatus(ctx, cli)
	if err != nil {
		return 0, errors.Wrap(err, "unable to get replset members")
	}

	membersLive := 0
	for _, member := range rsStatus.Members {
		var tags mongo.ReplsetTags
		for i := range cnf.Members {
			if member.Id == cnf.Members[i].ID {
				tags = cnf.Members[i].Tags
				break
			}
		}
		if _, ok := tags["external"]; ok {
			continue
		}

		switch member.State {
		case mongo.MemberStatePrimary, mongo.MemberStateSecondary, mongo.MemberStateArbiter:
			membersLive++
		case mongo.MemberStateStartup,
			mongo.MemberStateStartup2,
			mongo.MemberStateRecovering,
			mongo.MemberStateRollback,
			mongo.MemberStateDown,
			mongo.MemberStateUnknown:

			return 0, nil
		default:
			return 0, errors.Errorf("undefined state of the replset member %s: %v", member.Name, member.State)
		}
	}
	return membersLive, nil
}

func inShard(ctx context.Context, client *mgo.Client, rsName string) (bool, error) {
	shardList, err := mongo.ListShard(ctx, client)
	if err != nil {
		return false, errors.Wrap(err, "unable to get shard list")
	}

	for _, shard := range shardList.Shards {
		if shard.ID == rsName {
			return true, nil
		}
	}

	return false, nil
}

var errNoRunningMongodContainers = errors.New("no mongod containers in running state")

func mongoInitAdminUser(user, pwd string) string {
	return fmt.Sprintf("'db.getSiblingDB(\"admin\").createUser( "+
		"{"+
		"user: \"%s\","+
		"pwd: \"%s\","+
		"roles: [ \"userAdminAnyDatabase\" ]"+
		"})'", strings.ReplaceAll(user, "'", `'"'"'`), strings.ReplaceAll(pwd, "'", `'"'"'`))
}

func (r *ReconcilePerconaServerMongoDB) removeRSFromShard(ctx context.Context, cr *api.PerconaServerMongoDB, rsName string) error {
	log := logf.FromContext(ctx)

	if !cr.Spec.Sharding.Enabled {
		return nil
	}

	cli, err := r.mongosClientWithRole(ctx, cr, roleClusterAdmin)
	if err != nil {
		return errors.Errorf("failed to get mongos connection: %v", err)
	}

	defer func() {
		err := cli.Disconnect(ctx)
		if err != nil {
			log.Error(err, "failed to close mongos connection")
		}
	}()

	for {
		resp, err := mongo.RemoveShard(ctx, cli, rsName)
		if err != nil {
			return errors.Wrap(err, "remove shard")
		}

		if resp.State == mongo.ShardRemoveCompleted {
			log.Info(resp.Msg, "shard", rsName)
			return nil
		}

		log.Info(resp.Msg, "shard", rsName,
			"chunk remaining", resp.Remaining.Chunks, "jumbo chunks remaining", resp.Remaining.JumboChunks)

		time.Sleep(10 * time.Second)
	}
}

func (r *ReconcilePerconaServerMongoDB) handleRsAddToShard(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, rspod corev1.Pod,
	mongosPod corev1.Pod) error {
	if !isContainerAndPodRunning(rspod, "mongod") || !isPodReady(rspod) {
		return errors.Errorf("rsPod %s is not ready", rspod.Name)
	}
	if !isContainerAndPodRunning(mongosPod, "mongos") || !isPodReady(mongosPod) {
		return errors.New("mongos pod is not ready")
	}

	host, err := psmdb.MongoHost(ctx, r.client, cr, replset.Name, replset.Expose.Enabled, rspod)
	if err != nil {
		return errors.Wrapf(err, "get rsPod %s host", rspod.Name)
	}

	cli, err := r.mongosClientWithRole(ctx, cr, roleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "failed to get mongos client")
	}

	defer func() {
		err := cli.Disconnect(ctx)
		if err != nil {
			logf.FromContext(ctx).Error(err, "failed to close mongos connection")
		}
	}()

	err = mongo.AddShard(ctx, cli, replset.Name, host)
	if err != nil {
		return errors.Wrap(err, "failed to add shard")
	}

	return nil
}

// handleReplsetInit initializes the replset within the first running pod's mongod container.
// This must be ran from within the running container to utilize the MongoDB Localhost Exception.
//
// See: https://www.mongodb.com/docs/manual/core/localhost-exception/
func (r *ReconcilePerconaServerMongoDB) handleReplsetInit(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pods []corev1.Pod) error {
	log := logf.FromContext(ctx)

	for _, pod := range pods {
		if !isMongodPod(pod) || !isContainerAndPodRunning(pod, "mongod") || !isPodReady(pod) {
			continue
		}

		log.Info("initiating replset", "replset", replset.Name, "pod", pod.Name)

		host, err := psmdb.MongoHost(ctx, r.client, cr, replset.Name, replset.Expose.Enabled, pod)
		if err != nil {
			return fmt.Errorf("get host for the pod %s: %v", pod.Name, err)
		}
		var errb, outb bytes.Buffer

		err = r.clientcmd.Exec(&pod, "mongod", []string{"mongod", "--version"}, nil, &outb, &errb, false)
		if err != nil {
			return fmt.Errorf("exec --version: %v / %s / %s", err, outb.String(), errb.String())
		}

		mongoCmd := "mongosh"
		if !strings.Contains(outb.String(), "v6.0") {
			mongoCmd = "mongo"
		}

		if !cr.Spec.UnsafeConf {
			mongoCmd += " --tls --tlsCertificateKeyFile /tmp/tls.pem --tlsAllowInvalidCertificates --tlsCAFile /etc/mongodb-ssl/ca.crt"
		}

		cmd := []string{
			"sh", "-c",
			fmt.Sprintf(
				`
				cat <<-EOF | %s 
				rs.initiate(
					{
						_id: '%s',
						version: 1,
						members: [
							{ _id: 0, host: "%s" },
						]
					}
				)
				EOF
			`, mongoCmd, replset.Name, host),
		}

		errb.Reset()
		outb.Reset()
		err = r.clientcmd.Exec(&pod, "mongod", cmd, nil, &outb, &errb, false)
		if err != nil {
			return fmt.Errorf("exec rs.initiate: %v / %s / %s", err, outb.String(), errb.String())
		}

		time.Sleep(time.Second * 5)

		userAdmin, err := r.getInternalCredentials(ctx, cr, roleUserAdmin)
		if err != nil {
			return errors.Wrap(err, "failed to get userAdmin credentials")
		}

		cmd[2] = fmt.Sprintf(`%s --eval %s`, mongoCmd, mongoInitAdminUser(userAdmin.Username, userAdmin.Password))
		errb.Reset()
		outb.Reset()
		err = r.clientcmd.Exec(&pod, "mongod", cmd, nil, &outb, &errb, false)
		if err != nil {
			return fmt.Errorf("exec add admin user: %v / %s / %s", err, outb.String(), errb.String())
		}

		log.Info("replset initialized", "replset", replset.Name, "pod", pod.Name)

		return nil
	}

	return errNoRunningMongodContainers
}

func getRoles(cr *api.PerconaServerMongoDB, role UserRole) []map[string]interface{} {
	roles := make([]map[string]interface{}, 0)
	switch role {
	case roleDatabaseAdmin:
		return []map[string]interface{}{
			{"role": "readWriteAnyDatabase", "db": "admin"},
			{"role": "readAnyDatabase", "db": "admin"},
			{"role": "restore", "db": "admin"},
			{"role": "backup", "db": "admin"},
			{"role": "dbAdminAnyDatabase", "db": "admin"},
			{"role": string(roleClusterMonitor), "db": "admin"},
		}
	case roleClusterMonitor:
		if cr.CompareVersion("1.12.0") >= 0 {
			roles = []map[string]interface{}{
				{"db": "admin", "role": "explainRole"},
				{"db": "local", "role": "read"},
			}
		}
	case roleBackup:
		roles = []map[string]interface{}{
			{"db": "admin", "role": "readWrite"},
			{"db": "admin", "role": string(roleClusterMonitor)},
			{"db": "admin", "role": "restore"},
			{"db": "admin", "role": "pbmAnyAction"},
		}
	}
	roles = append(roles, map[string]interface{}{"db": "admin", "role": string(role)})
	return roles
}

// compareResources compares two map[string]interface{} values and returns true if they are equal
func compareResources(x, y map[string]interface{}) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if !reflect.DeepEqual(y[k], v) {
			return false
		}
	}
	return true
}

// compareSlices compares two non-sorted string slices, returns true if they have the same values
func compareSlices(x, y []string) bool {
	if len(x) != len(y) {
		return false
	}
	xMap := make(map[string]struct{}, len(x))
	for _, v := range x {
		xMap[v] = struct{}{}
	}
	for _, v := range y {
		if _, ok := xMap[v]; !ok {
			return false
		}
	}
	return true
}

// comparePrivileges compares 2 RolePrivilege arrays and returns true if they are equal
func comparePrivileges(x []mongo.RolePrivilege, y []mongo.RolePrivilege) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if !(compareResources(x[i].Resource, y[i].Resource) && compareSlices(x[i].Actions, y[i].Actions)) {
			return false
		}
	}
	return true
}

func (r *ReconcilePerconaServerMongoDB) createOrUpdateSystemRoles(ctx context.Context, cli *mgo.Client, role string, privileges []mongo.RolePrivilege) error {
	roleInfo, err := mongo.GetRole(ctx, cli, role)
	if err != nil {
		return errors.Wrap(err, "mongo get role")
	}
	if roleInfo == nil {
		err = mongo.CreateRole(ctx, cli, role, privileges, []interface{}{})
		return errors.Wrapf(err, "create role %s", role)
	}
	if !comparePrivileges(privileges, roleInfo.Privileges) {
		err = mongo.UpdateRole(ctx, cli, role, privileges, []interface{}{})
		return errors.Wrapf(err, "update role")
	}
	return nil
}

// compareRoles compares 2 role arrays and returns true if they are equal
func compareRoles(x []map[string]interface{}, y []map[string]interface{}) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if !reflect.DeepEqual(x[i], y[i]) {
			return false
		}
	}
	return true
}

func (r *ReconcilePerconaServerMongoDB) createOrUpdateSystemUsers(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) error {
	log := logf.FromContext(ctx)

	cli, err := r.mongoClientWithRole(ctx, cr, *replset, roleUserAdmin)
	if err != nil {
		return errors.Wrap(err, "failed to get mongo client")
	}

	defer func() {
		err := cli.Disconnect(ctx)
		if err != nil {
			log.Error(err, "failed to close mongo connection")
		}
	}()

	if cr.CompareVersion("1.12.0") >= 0 {
		err = r.createOrUpdateSystemRoles(ctx, cli, "explainRole",
			[]mongo.RolePrivilege{{
				Resource: map[string]interface{}{
					"db":         "",
					"collection": "system.profile",
				},
				Actions: []string{
					"listIndexes",
					"listCollections",
					"dbStats",
					"dbHash",
					"collStats",
					"find",
				},
			}})
		if err != nil {
			return errors.Wrap(err, "create or update system role")
		}
	}

	err = r.createOrUpdateSystemRoles(ctx, cli, "pbmAnyAction",
		[]mongo.RolePrivilege{{
			Resource: map[string]interface{}{"anyResource": true},
			Actions:  []string{"anyAction"},
		}})
	if err != nil {
		return errors.Wrap(err, "create or update system role")
	}

	users := []UserRole{roleClusterAdmin, roleClusterMonitor, roleBackup}
	if cr.CompareVersion("1.13.0") >= 0 {
		users = append(users, roleDatabaseAdmin)
	}

	for _, role := range users {
		creds, err := r.getInternalCredentials(ctx, cr, role)
		if err != nil {
			log.Error(err, "failed to get credentials", "role", role)
			continue
		}
		user, err := mongo.GetUserInfo(ctx, cli, creds.Username)
		if err != nil {
			return errors.Wrap(err, "get user info")
		}
		if user == nil {
			err = mongo.CreateUser(ctx, cli, creds.Username, creds.Password, getRoles(cr, role)...)
			if err != nil {
				return errors.Wrapf(err, "failed to create user %s", role)
			}
			continue
		}
		if !compareRoles(user.Roles, getRoles(cr, role)) {
			err = mongo.UpdateUserRoles(ctx, cli, creds.Username, getRoles(cr, role))
			if err != nil {
				return errors.Wrapf(err, "failed to create user %s", role)
			}
		}
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) recoverReplsetNoPrimary(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pod corev1.Pod) error {
	host, err := psmdb.MongoHost(ctx, r.client, cr, replset.Name, replset.Expose.Enabled, pod)
	if err != nil {
		return errors.Wrapf(err, "get mongo hostname for pod/%s", pod.Name)
	}

	cli, err := r.standaloneClientWithRole(ctx, cr, roleClusterAdmin, host)
	if err != nil {
		return errors.Wrap(err, "get standalone client")
	}

	cnf, err := mongo.ReadConfig(ctx, cli)
	if err != nil {
		return errors.Wrap(err, "get mongo config")
	}

	for i := 0; i < len(cnf.Members); i++ {
		tags := []mongo.ConfigMember(cnf.Members)[i].Tags
		podName, ok := tags["podName"]
		if !ok {
			continue
		}

		[]mongo.ConfigMember(cnf.Members)[i].Host = replset.PodFQDNWithPort(cr, podName)
	}

	cnf.Version++
	logf.FromContext(ctx).Info("Writing replicaset config", "config", cnf)

	if err := mongo.WriteConfig(ctx, cli, cnf); err != nil {
		return errors.Wrap(err, "write mongo config")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) restoreInProgress(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) (bool, error) {
	sts := appsv1.StatefulSet{}
	stsName := cr.Name + "-" + replset.Name
	nn := types.NamespacedName{Name: stsName, Namespace: cr.Namespace}
	if err := r.client.Get(ctx, nn, &sts); err != nil {
		return false, errors.Wrapf(err, "get statefulset %s", stsName)
	}
	_, ok := sts.Annotations[api.AnnotationRestoreInProgress]
	return ok, nil
}

// isMongodPod returns a boolean reflecting if a pod
// is running a mongod container
func isMongodPod(pod corev1.Pod) bool {
	return getPodContainer(&pod, "mongod") != nil
}

func getPodContainer(pod *corev1.Pod, containerName string) *corev1.Container {
	for _, cont := range pod.Spec.Containers {
		if cont.Name == containerName {
			return &cont
		}
	}
	return nil
}

// isContainerAndPodRunning returns a boolean reflecting if
// a container and pod are in a running state
func isContainerAndPodRunning(pod corev1.Pod, containerName string) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name == containerName && container.State.Running != nil {
			return true
		}
	}
	return false
}

// isPodReady returns a boolean reflecting if a pod is in a "ready" state
func isPodReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		if condition.Type == corev1.PodReady {
			return true
		}
	}
	return false
}
