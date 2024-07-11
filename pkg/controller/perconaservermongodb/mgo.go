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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

var errReplsetLimit = fmt.Errorf("maximum replset member (%d) count reached", mongo.MaxMembers)

func (r *ReconcilePerconaServerMongoDB) reconcileCluster(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec,
	mongosPods []corev1.Pod,
) (api.AppState, error) {
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

	pods, err := psmdb.GetRSPods(ctx, r.client, cr, replset.Name)
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

	cli, err := r.mongoClientWithRole(ctx, cr, *replset, api.RoleClusterAdmin)
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
	defer func() {
		if err := cli.Disconnect(ctx); err != nil {
			log.Error(err, "failed to close connection")
		}
	}()

	if cr.Spec.Unmanaged {
		status, err := cli.RSStatus(ctx)
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
		len(mongosPods) > 0 && cr.Spec.Sharding.Mongos.Size > 0 {

		mongosSession, err := r.mongosClientWithRole(ctx, cr, api.RoleClusterAdmin)
		if err != nil {
			return api.AppStateError, errors.Wrap(err, "failed to get mongos connection")
		}

		defer func() {
			err := mongosSession.Disconnect(ctx)
			if err != nil {
				log.Error(err, "failed to close mongos connection")
			}
		}()

		err = mongosSession.SetDefaultRWConcern(ctx, mongo.DefaultReadConcern, mongo.DefaultWriteConcern)
		// SetDefaultRWConcern introduced in MongoDB 4.4
		if err != nil && !strings.Contains(err.Error(), "CommandNotFound") {
			return api.AppStateError, errors.Wrap(err, "set default RW concern")
		}

		rsName := replset.Name
		name, err := replset.CustomReplsetName()
		if err == nil {
			rsName = name
		}

		in, err := inShard(ctx, mongosSession, rsName)
		if err != nil {
			return api.AppStateError, errors.Wrap(err, "get shard")
		}

		if !in {
			log.Info("adding rs to shard", "rs", rsName)
			err := r.handleRsAddToShard(ctx, cr, replset, pods.Items[0], mongosPods[0])
			if err != nil {
				return api.AppStateError, errors.Wrap(err, "add shard")
			}

			log.Info("added to shard", "rs", rsName)
		}

		rs := cr.Status.Replsets[replset.Name]
		t := true
		rs.AddedAsShard = &t
		cr.Status.Replsets[replset.Name] = rs

	}

	if replset.Arbiter.Enabled && !cr.Spec.Sharding.Enabled {
		err := cli.SetDefaultRWConcern(ctx, mongo.DefaultReadConcern, mongo.DefaultWriteConcern)
		// SetDefaultRWConcern introduced in MongoDB 4.4
		if err != nil && !strings.Contains(err.Error(), "CommandNotFound") {
			return api.AppStateError, errors.Wrap(err, "set default RW concern")
		}
	}

	membersLive, err := r.updateConfigMembers(ctx, cli, cr, replset)
	if err != nil {
		return api.AppStateError, errors.Wrap(err, "failed to update config members")
	}

	if err := r.addHorizons(ctx, cli, replset); err != nil {
		return api.AppStateError, errors.Wrap(err, "failed to add horizons")
	}

	if membersLive == len(pods.Items) {
		return api.AppStateReady, nil
	}

	return api.AppStateInit, nil
}

func (r *ReconcilePerconaServerMongoDB) updateConfigMembers(ctx context.Context, cli mongo.Client, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) (int, error) {
	log := logf.FromContext(ctx)
	// Primary with a Secondary and an Arbiter (PSA)
	unsafePSA := false

	if cr.CompareVersion("1.15.0") <= 0 {
		unsafePSA = cr.Spec.UnsafeConf && rs.Arbiter.Enabled && rs.Arbiter.Size == 1 && !rs.NonVoting.Enabled && rs.Size == 2
	} else {
		unsafePSA = cr.Spec.Unsafe.ReplsetSize && rs.Arbiter.Enabled && rs.Arbiter.Size == 1 && !rs.NonVoting.Enabled && rs.Size == 2
	}

	pods, err := psmdb.GetRSPods(ctx, r.client, cr, rs.Name)
	if err != nil {
		return 0, errors.Wrap(err, "get rs pods")
	}

	cnf, err := cli.ReadConfig(ctx)
	if err != nil {
		return 0, errors.Wrap(err, "get mongo config")
	}

	existingHorizons := make([]string, 0)
	for _, member := range cnf.Members {
		if len(member.Horizons) > 0 {
			for name := range member.Horizons {
				existingHorizons = append(existingHorizons, name)
			}
		}
	}

	members := mongo.ConfigMembers{}
	for key, pod := range pods.Items {
		if key >= mongo.MaxMembers {
			log.Error(errReplsetLimit, "rs", rs.Name)
			break
		}

		host, err := psmdb.MongoHost(ctx, r.client, cr, cr.Spec.ClusterServiceDNSMode, rs.Name, rs.Expose.Enabled, pod)
		if err != nil {
			return 0, fmt.Errorf("get host for pod %s: %v", pod.Name, err)
		}

		nodeLabels := mongo.ReplsetTags{
			"nodeName":    pod.Spec.NodeName,
			"podName":     pod.Name,
			"serviceName": cr.Name,
		}

		labels, err := psmdb.GetNodeLabels(ctx, r.client, cr, pod)
		if err == nil {
			nodeLabels = util.MapMerge(nodeLabels, mongo.ReplsetTags{
				"region": labels[corev1.LabelTopologyRegion],
				"zone":   labels[corev1.LabelTopologyZone],
			})
		}

		member := mongo.ConfigMember{
			ID:           key,
			Host:         host,
			BuildIndexes: true,
			Priority:     mongo.DefaultPriority,
			Votes:        mongo.DefaultVotes,
		}

		if len(existingHorizons) > 0 {
			for _, horizon := range existingHorizons {
				if _, ok := rs.Horizons[pod.Name][horizon]; !ok {
					return 0, errors.Errorf("horizon %s is missing for pod %s", horizon, pod.Name)
				}

				member.Horizons = map[string]string{
					horizon: rs.Horizons[pod.Name][horizon],
				}
			}
		}

		switch pod.Labels["app.kubernetes.io/component"] {
		case "arbiter":
			member.ArbiterOnly = true
			member.Priority = 0
		case "mongod", "cfg":
			member.Tags = nodeLabels
		case "nonVoting":
			member.Tags = util.MapMerge(mongo.ReplsetTags{
				"nonVoting": "true",
			}, nodeLabels)
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

		if err := cli.WriteConfig(ctx, cnf); err != nil {
			return 0, errors.Wrap(err, "fix tags: write mongo config")
		}
	}

	if cnf.Members.FixHosts(members) {
		cnf.Version++

		log.Info("Fixing member hosts", "replset", rs.Name)

		err = cli.WriteConfig(ctx, cnf)
		if err != nil {
			return 0, errors.Wrap(err, "fix hosts: write mongo config")
		}
	}

	if cnf.Members.RemoveOld(members) {
		cnf.Version++

		log.Info("Removing old nodes", "replset", rs.Name)

		err = cli.WriteConfig(ctx, cnf)
		if err != nil {
			return 0, errors.Wrap(err, "delete: write mongo config")
		}
	}

	if cnf.Members.AddNew(members) {
		cnf.Version++

		log.Info("Adding new nodes", "replset", rs.Name)

		err = cli.WriteConfig(ctx, cnf)
		if err != nil {
			return 0, errors.Wrap(err, "add new: write mongo config")
		}
	}

	if cnf.Members.ExternalNodesChanged(members) {
		cnf.Version++

		log.Info("Updating external nodes", "replset", rs.Name)

		err = cli.WriteConfig(ctx, cnf)
		if err != nil {
			return 0, errors.Wrap(err, "update external nodes: write mongo config")
		}
	}

	currMembers := append(mongo.ConfigMembers(nil), cnf.Members...)
	cnf.Members.SetVotes(unsafePSA)
	if !reflect.DeepEqual(currMembers, cnf.Members) {
		cnf.Version++

		log.Info("Configuring member votes and priorities", "replset", rs.Name)

		err := cli.WriteConfig(ctx, cnf)
		if err != nil {
			return 0, errors.Wrap(err, "set votes: write mongo config")
		}
	}

	rsStatus, err := cli.RSStatus(ctx)
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

func (r *ReconcilePerconaServerMongoDB) addHorizons(ctx context.Context, cli mongo.Client, rs *api.ReplsetSpec) error {
	if len(rs.Horizons) == 0 {
		return nil
	}

	log := logf.FromContext(ctx)

	cnf, err := cli.ReadConfig(ctx)
	if err != nil {
		return errors.Wrap(err, "get mongo config")
	}

	if len(cnf.Members) != int(rs.Size) {
		log.V(1).Info("Waiting for all members to be added to config", "members", len(cnf.Members), "expected", rs.Size)
		return nil
	}

	members := make([]mongo.ConfigMember, len(cnf.Members))
	copy(members, cnf.Members)

	for i := 0; i < len(members); i++ {
		member := []mongo.ConfigMember(members)[i]
		horizons := make(map[string]string)
		for h, domain := range rs.Horizons[member.Tags["podName"]] {
			d := domain
			if !strings.Contains(d, ":") {
				d = fmt.Sprintf("%s:%d", d, api.DefaultMongodPort)
			}
			horizons[h] = d
		}

		[]mongo.ConfigMember(members)[i].Horizons = horizons
	}

	if cnf.Members.HorizonsChanged(members) {
		cnf.Version++

		log.Info("Updating horizons", "replset", rs.Name)

		err = cli.WriteConfig(ctx, cnf)
		if err != nil {
			return errors.Wrap(err, "update horizons: write mongo config")
		}
	}

	return nil
}

func inShard(ctx context.Context, client mongo.Client, rsName string) (bool, error) {
	shardList, err := client.ListShard(ctx)
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

	cli, err := r.mongosClientWithRole(ctx, cr, api.RoleClusterAdmin)
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
		resp, err := cli.RemoveShard(ctx, rsName)
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
	mongosPod corev1.Pod,
) error {
	if !isContainerAndPodRunning(rspod, "mongod") || !isPodReady(rspod) {
		return errors.Errorf("rsPod %s is not ready", rspod.Name)
	}
	if !isContainerAndPodRunning(mongosPod, "mongos") || !isPodReady(mongosPod) {
		return errors.New("mongos pod is not ready")
	}

	host, err := psmdb.MongoHost(ctx, r.client, cr, cr.Spec.ClusterServiceDNSMode, replset.Name, replset.Expose.Enabled, rspod)
	if err != nil {
		return errors.Wrapf(err, "get rsPod %s host", rspod.Name)
	}

	cli, err := r.mongosClientWithRole(ctx, cr, api.RoleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "failed to get mongos client")
	}

	defer func() {
		err := cli.Disconnect(ctx)
		if err != nil {
			logf.FromContext(ctx).Error(err, "failed to close mongos connection")
		}
	}()

	rsName := replset.Name
	name, err := replset.CustomReplsetName()
	if err == nil {
		rsName = name
	}

	err = cli.AddShard(ctx, rsName, host)
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

		replsetName := replset.Name
		name, err := replset.CustomReplsetName()
		if err == nil {
			replsetName = name
		}

		log.Info("initiating replset", "replset", replsetName, "pod", pod.Name)

		host, err := psmdb.MongoHost(ctx, r.client, cr, cr.Spec.ClusterServiceDNSMode, replset.Name, replset.Expose.Enabled, pod)
		if err != nil {
			return fmt.Errorf("get host for the pod %s: %v", pod.Name, err)
		}

		var errb, outb bytes.Buffer

		err = r.clientcmd.Exec(ctx, &pod, "mongod", []string{"mongod", "--version"}, nil, &outb, &errb, false)
		if err != nil {
			return fmt.Errorf("exec --version: %v / %s / %s", err, outb.String(), errb.String())
		}

		mongoCmd := "mongosh"
		if strings.Contains(outb.String(), "v4.4") || strings.Contains(outb.String(), "v5.0") {
			mongoCmd = "mongo"
		}

		if cr.TLSEnabled() {
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
			`, mongoCmd, replsetName, host),
		}

		errb.Reset()
		outb.Reset()
		err = r.clientcmd.Exec(ctx, &pod, "mongod", cmd, nil, &outb, &errb, false)
		if err != nil {
			return fmt.Errorf("exec rs.initiate: %v / %s / %s", err, outb.String(), errb.String())
		}

		time.Sleep(time.Second * 5)

		userAdmin, err := getInternalCredentials(ctx, r.client, cr, api.RoleUserAdmin)
		if err != nil {
			return errors.Wrap(err, "failed to get userAdmin credentials")
		}

		cmd[2] = fmt.Sprintf(`%s --eval %s`, mongoCmd, mongoInitAdminUser(userAdmin.Username, userAdmin.Password))
		errb.Reset()
		outb.Reset()
		err = r.clientcmd.Exec(ctx, &pod, "mongod", cmd, nil, &outb, &errb, false)
		if err != nil {
			return fmt.Errorf("exec add admin user: %v / %s / %s", err, outb.String(), errb.String())
		}

		log.Info("replset initialized", "replset", replsetName, "pod", pod.Name)

		return nil
	}

	return errNoRunningMongodContainers
}

func getRoles(cr *api.PerconaServerMongoDB, role api.UserRole) []map[string]interface{} {
	roles := make([]map[string]interface{}, 0)
	switch role {
	case api.RoleDatabaseAdmin:
		return []map[string]interface{}{
			{"role": "readWriteAnyDatabase", "db": "admin"},
			{"role": "readAnyDatabase", "db": "admin"},
			{"role": "restore", "db": "admin"},
			{"role": "backup", "db": "admin"},
			{"role": "dbAdminAnyDatabase", "db": "admin"},
			{"role": string(api.RoleClusterMonitor), "db": "admin"},
		}
	case api.RoleClusterMonitor:
		if cr.CompareVersion("1.12.0") >= 0 {
			roles = []map[string]interface{}{
				{"db": "admin", "role": "explainRole"},
				{"db": "local", "role": "read"},
			}
		}
	case api.RoleBackup:
		roles = []map[string]interface{}{
			{"db": "admin", "role": "readWrite"},
			{"db": "admin", "role": string(api.RoleClusterMonitor)},
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

func (r *ReconcilePerconaServerMongoDB) createOrUpdateSystemRoles(ctx context.Context, cli mongo.Client, role string, privileges []mongo.RolePrivilege) error {
	roleInfo, err := cli.GetRole(ctx, role)
	if err != nil {
		return errors.Wrap(err, "mongo get role")
	}
	if roleInfo == nil {
		err = cli.CreateRole(ctx, role, privileges, []interface{}{})
		return errors.Wrapf(err, "create role %s", role)
	}
	if !comparePrivileges(privileges, roleInfo.Privileges) {
		err = cli.UpdateRole(ctx, role, privileges, []interface{}{})
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

	cli, err := r.mongoClientWithRole(ctx, cr, *replset, api.RoleUserAdmin)
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
		privileges := []mongo.RolePrivilege{
			{
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
			},
		}
		if cr.CompareVersion("1.15.0") >= 0 {
			privileges = []mongo.RolePrivilege{
				{
					Resource: map[string]interface{}{
						"db":         "",
						"collection": "",
					},
					Actions: []string{
						"listIndexes",
						"listCollections",
						"dbStats",
						"dbHash",
						"collStats",
						"find",
					},
				},
				{
					Resource: map[string]interface{}{
						"db":         "",
						"collection": "system.profile",
					},
					Actions: []string{
						"indexStats",
						"dbStats",
						"collStats",
					},
				},
			}
		}
		if cr.CompareVersion("1.16.0") >= 0 {
			privileges = append(privileges, mongo.RolePrivilege{
				Resource: map[string]interface{}{
					"db":         "admin",
					"collection": "system.version",
				},
				Actions: []string{
					"find",
				},
			})
		}

		err = r.createOrUpdateSystemRoles(ctx, cli, "explainRole", privileges)
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

	users := []api.UserRole{api.RoleClusterAdmin, api.RoleClusterMonitor, api.RoleBackup}
	if cr.CompareVersion("1.13.0") >= 0 {
		users = append(users, api.RoleDatabaseAdmin)
	}

	for _, role := range users {
		creds, err := getInternalCredentials(ctx, r.client, cr, role)
		if err != nil {
			log.Error(err, "failed to get credentials", "role", role)
			continue
		}
		user, err := cli.GetUserInfo(ctx, creds.Username)
		if err != nil {
			return errors.Wrap(err, "get user info")
		}
		if user == nil {
			err = cli.CreateUser(ctx, creds.Username, creds.Password, getRoles(cr, role)...)
			if err != nil {
				return errors.Wrapf(err, "failed to create user %s", role)
			}
			continue
		}
		if !compareRoles(user.Roles, getRoles(cr, role)) {
			err = cli.UpdateUserRoles(ctx, creds.Username, getRoles(cr, role))
			if err != nil {
				return errors.Wrapf(err, "failed to create user %s", role)
			}
		}
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
