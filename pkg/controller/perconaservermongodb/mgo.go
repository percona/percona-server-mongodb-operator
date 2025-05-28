package perconaservermongodb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/x/mongo/driver/topology"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

var errReplsetLimit = fmt.Errorf("maximum replset member (%d) count reached", mongo.MaxMembers)

func (r *ReconcilePerconaServerMongoDB) reconcileCluster(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, mongosPods []corev1.Pod) (api.AppState, map[string]api.ReplsetMemberStatus, error) {
	log := logf.FromContext(ctx)

	replsetSize := replset.GetSize()

	restoreInProgress, err := r.restoreInProgress(ctx, cr, replset)
	if err != nil {
		return api.AppStateError, nil, errors.Wrap(err, "check if restore in progress")
	}

	if restoreInProgress {
		return api.AppStateInit, nil, nil
	}

	if replsetSize == 0 {
		return api.AppStateReady, nil, nil
	}

	pods, err := psmdb.GetRSPods(ctx, r.client, cr, replset.Name)
	if err != nil {
		return api.AppStateInit, nil, errors.Wrap(err, "failed to get replset pods")
	}

	// all pods needs to be scheduled to reconcile
	if int(replsetSize) > len(pods.Items) {
		for _, pod := range pods.Items {
			for _, containerStatus := range pod.Status.ContainerStatuses {
				if containerStatus.State.Waiting != nil && containerStatus.State.Waiting.Reason == "CrashLoopBackOff" {
					return api.AppStateError, nil, errors.Errorf("pod %s is in CrashLoopBackOff state", pod.Name)
				}
			}
		}
		log.Info("Waiting for the pods", "replset", replset.Name, "size", replsetSize, "pods", len(pods.Items))
		return api.AppStateInit, nil, nil
	}

	if cr.MCSEnabled() {
		seList, err := psmdb.GetExportedServices(ctx, r.client, cr)
		if err != nil {
			return api.AppStateError, nil, errors.Wrap(err, "get exported services")
		}

		if len(seList.Items) == 0 {
			log.Info("waiting for service exports")
			return api.AppStateInit, nil, nil
		}

		for _, se := range seList.Items {
			imported, err := psmdb.IsServiceImported(ctx, r.client, cr, se.Name)
			if err != nil {
				return api.AppStateError, nil, errors.Wrapf(err, "check if service is imported for %s", se.Name)
			}
			if !imported {
				log.Info("waiting for service import", "replset", replset.Name, "serviceExport", se.Name)
				return api.AppStateInit, nil, nil
			}
		}
	}

	cli, err := r.mongoClientWithRole(ctx, cr, replset, api.RoleClusterAdmin)
	if err != nil {
		if cr.Spec.Unmanaged {
			return api.AppStateInit, nil, nil
		}
		if cr.Status.Replsets[replset.Name].Initialized {
			if errors.Is(err, topology.ErrServerSelectionTimeout) && strings.Contains(err.Error(), "ReplicaSetNoPrimary") {
				log.Error(err, "FULL CLUSTER CRASH")

				err := r.handleReplicaSetNoPrimary(ctx, cr, replset, pods.Items)
				if err != nil {
					return api.AppStateError, nil, errors.Wrap(err, "handle ReplicaSetNoPrimary")
				}

				return api.AppStateError, nil, nil
			}

			return api.AppStateError, nil, errors.Wrap(err, "dial")
		}

		pod, primary, err := r.handleReplsetInit(ctx, cr, replset, pods.Items)
		if err != nil {
			if errors.Is(err, errNoRunningMongodContainers) {
				return api.AppStateInit, nil, nil
			}
			return api.AppStateInit, nil, errors.Wrap(err, "handleReplsetInit")
		}

		err = r.createOrUpdateSystemUsers(ctx, cr, replset)
		if err != nil {
			return api.AppStateInit, nil, errors.Wrap(err, "create system users")
		}

		rs := cr.Status.Replsets[replset.Name]
		rs.Initialized = true
		rs.Members = map[string]api.ReplsetMemberStatus{pod.Name: *primary}
		cr.Status.Replsets[replset.Name] = rs

		cr.Status.AddCondition(api.ClusterCondition{
			Status:             api.ConditionTrue,
			Type:               api.AppStateInit,
			Message:            replset.Name,
			LastTransitionTime: metav1.NewTime(time.Now()),
		})

		return api.AppStateInit, rs.Members, nil
	}
	defer func() {
		if err := cli.Disconnect(ctx); err != nil {
			log.Error(err, "failed to close connection")
		}
	}()

	if cr.Spec.Unmanaged {
		status, err := cli.RSStatus(ctx)
		if err != nil {
			return api.AppStateError, nil, errors.Wrap(err, "failed to get rs status")
		}
		if status.Primary() == nil {
			return api.AppStateInit, nil, nil
		}
		return api.AppStateReady, nil, nil
	}
	err = r.createOrUpdateSystemUsers(ctx, cr, replset)
	if err != nil {
		return api.AppStateInit, nil, errors.Wrap(err, "create system users")
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

		return api.AppStateInit, nil, nil
	}

	rstRunning, err := r.isRestoreRunning(ctx, cr)
	if err != nil {
		return api.AppStateInit, nil, errors.Wrap(err, "failed to check running restore")
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
			return api.AppStateError, nil, errors.Wrap(err, "failed to get mongos connection")
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
			return api.AppStateError, nil, errors.Wrap(err, "set default RW concern")
		}

		rsName := replset.Name
		name, err := replset.CustomReplsetName()
		if err == nil {
			rsName = name
		}

		in, err := inShard(ctx, mongosSession, rsName)
		if err != nil {
			return api.AppStateError, nil, errors.Wrap(err, "get shard")
		}

		if !in {
			log.Info("adding rs to shard", "rs", rsName)
			err := r.handleRsAddToShard(ctx, cr, replset, pods.Items[0], mongosPods[0])
			if err != nil {
				return api.AppStateError, nil, errors.Wrap(err, "add shard")
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
			return api.AppStateError, nil, errors.Wrap(err, "set default RW concern")
		}
	}

	rsMembers, liveMembers, err := r.updateConfigMembers(ctx, cli, cr, replset)
	if err != nil {
		return api.AppStateError, nil, errors.Wrap(err, "failed to update config members")
	}

	if liveMembers == len(pods.Items) {
		return api.AppStateReady, rsMembers, nil
	}

	log.V(1).Info("Replset is not ready", "liveMembers", liveMembers, "pods", len(pods.Items))

	return api.AppStateInit, rsMembers, nil
}

func (r *ReconcilePerconaServerMongoDB) getConfigMemberForPod(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, id int, pod *corev1.Pod) (mongo.ConfigMember, error) {
	host, err := psmdb.MongoHost(ctx, r.client, cr, cr.Spec.ClusterServiceDNSMode, rs, rs.Expose.Enabled, *pod)
	if err != nil {
		return mongo.ConfigMember{}, errors.Wrapf(err, "get host for pod %s", pod.Name)
	}

	member := mongo.ConfigMember{
		ID:           id,
		Host:         host,
		BuildIndexes: true,
		Priority:     mongo.DefaultPriority,
		Votes:        mongo.DefaultVotes,
	}

	overrides := rs.ReplsetOverrides[pod.Name]

	if overrides.Priority != nil {
		member.Priority = *overrides.Priority
	}

	horizons := make(map[string]string)
	for h, domain := range rs.Horizons[pod.Name] {
		d := domain
		if !strings.Contains(d, ":") {
			d = fmt.Sprintf("%s:%d", d, rs.GetPort())
		}
		horizons[h] = d
	}
	for h, domain := range overrides.Horizons {
		d := domain
		if !strings.Contains(d, ":") {
			d = fmt.Sprintf("%s:%d", d, rs.GetPort())
		}
		horizons[h] = d
	}
	if len(horizons) > 0 {
		member.Horizons = horizons
	}

	tags := util.MapMerge(mongo.ReplsetTags{
		"nodeName":    pod.Spec.NodeName,
		"podName":     pod.Name,
		"serviceName": cr.Name,
	}, overrides.Tags)

	labels, err := psmdb.GetNodeLabels(ctx, r.client, cr, *pod)
	if err == nil {
		tags = util.MapMerge(tags, mongo.ReplsetTags{
			"region": labels[corev1.LabelTopologyRegion],
			"zone":   labels[corev1.LabelTopologyZone],
		})
	}

	if compareTags(tags, rs.PrimaryPreferTagSelector) {
		member.Priority = mongo.DefaultPriority + 1
	}

	switch pod.Labels[naming.LabelKubernetesComponent] {
	case naming.ComponentArbiter:
		member.ArbiterOnly = true
		member.Priority = 0
	case naming.ComponentMongod, "cfg":
		member.Tags = tags
	case naming.ComponentNonVoting:
		member.Tags = util.MapMerge(mongo.ReplsetTags{
			naming.ComponentNonVoting: "true",
		}, tags)
		member.Priority = 0
		member.Votes = 0
	case naming.ComponentHidden:
		member.Tags = util.MapMerge(mongo.ReplsetTags{
			naming.ComponentHidden: "true",
		}, tags)
		member.Priority = 0
		member.Votes = 1
	}

	return member, nil
}

func (r *ReconcilePerconaServerMongoDB) getConfigMemberForExternalNode(id int, extNode api.ExternalNode) mongo.ConfigMember {
	member := mongo.ConfigMember{
		ID:           id,
		Votes:        extNode.Votes,
		Priority:     extNode.Priority,
		BuildIndexes: true,
		Tags:         mongo.ReplsetTags{"external": "true"},
	}

	if strings.Contains(extNode.Host, ":") {
		member.Host = extNode.Host
	} else {
		member.Host = extNode.HostPort()
	}

	for k, v := range extNode.Tags {
		member.Tags[k] = v
	}

	horizons := make(map[string]string)
	for h, domain := range extNode.Horizons {
		d := domain
		if !strings.Contains(d, ":") {
			d = fmt.Sprintf("%s:%d", d, api.DefaultMongoPort)
		}
		horizons[h] = d
	}
	if len(horizons) > 0 {
		member.Horizons = horizons
	}

	return member
}

func (r *ReconcilePerconaServerMongoDB) updateConfigMembers(ctx context.Context, cli mongo.Client, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) (map[string]api.ReplsetMemberStatus, int, error) {
	log := logf.FromContext(ctx)
	// Primary with a Secondary and an Arbiter (PSA)
	unsafePSA := false
	rsMembers := make(map[string]api.ReplsetMemberStatus)

	if cr.CompareVersion("1.15.0") <= 0 {
		unsafePSA = cr.Spec.UnsafeConf && rs.Arbiter.Enabled && rs.Arbiter.Size == 1 && !rs.NonVoting.Enabled && rs.Size == 2
	} else {
		unsafePSA = cr.Spec.Unsafe.ReplsetSize && rs.Arbiter.Enabled && rs.Arbiter.Size == 1 && !rs.NonVoting.Enabled && rs.Size == 2
	}

	pods, err := psmdb.GetRSPods(ctx, r.client, cr, rs.Name)
	if err != nil {
		return rsMembers, 0, errors.Wrap(err, "get rs pods")
	}

	cnf, err := cli.ReadConfig(ctx)
	if err != nil {
		return rsMembers, 0, errors.Wrap(err, "get replset config")
	}

	rsStatus, err := cli.RSStatus(ctx)
	if err != nil {
		return rsMembers, 0, errors.Wrap(err, "get replset status")
	}

	members := mongo.ConfigMembers{}
	for key, pod := range pods.Items {
		if key >= mongo.MaxMembers {
			log.Error(errReplsetLimit, "rs", rs.Name)
			break
		}

		member, err := r.getConfigMemberForPod(ctx, cr, rs, key, &pod)
		if err != nil {
			return rsMembers, 0, errors.Wrapf(err, "get config member for pod %s", pod.Name)
		}

		members = append(members, member)
	}

	// sort config members by priority, descending
	sort.Slice(members, func(i, j int) bool {
		return members[i].Priority > members[j].Priority
	})

	memberC := len(members)
	for i, extNode := range rs.ExternalNodes {
		if memberC+i+1 >= mongo.MaxMembers {
			log.Error(errReplsetLimit, "rs", rs.Name)
			break
		}

		members = append(members, r.getConfigMemberForExternalNode(memberC+i, *extNode))
	}

	if member, changed := cnf.Members.FixMemberHostnames(ctx, members, rsStatus); changed {
		if member.State == mongo.MemberStatePrimary {
			log.Info("Stepping down the primary", "member", member.Name)
			if err := cli.StepDown(ctx, 60, false); err != nil {
				return rsMembers, 0, errors.Wrap(err, "step down primary")
			}
		}

		cnf.Version++

		log.Info("Fixing hostname of member", "replset", rs.Name, "id", member.Id, "member", member.Name)

		if err := cli.WriteConfig(ctx, cnf, false); err != nil {
			return rsMembers, 0, errors.Wrap(err, "fix member hostname: write mongo config")
		}

		return rsMembers, 0, nil
	}

	if cnf.Members.FixMemberConfigs(ctx, members) {
		cnf.Version++

		log.Info("Fixing member configurations", "replset", rs.Name)

		if err := cli.WriteConfig(ctx, cnf, false); err != nil {
			return rsMembers, 0, errors.Wrap(err, "fix member configurations: write mongo config")
		}
	}

	if cnf.Members.RemoveOld(ctx, members) {
		cnf.Version++

		log.Info("Removing old nodes", "replset", rs.Name)

		err = cli.WriteConfig(ctx, cnf, false)
		if err != nil {
			return rsMembers, 0, errors.Wrap(err, "delete: write mongo config")
		}
	}

	if cnf.Members.AddNew(ctx, members) {
		cnf.Version++

		log.Info("Adding new nodes", "replset", rs.Name)

		err = cli.WriteConfig(ctx, cnf, false)
		if err != nil {
			return rsMembers, 0, errors.Wrap(err, "add new: write mongo config")
		}
	}

	if cnf.Members.ExternalNodesChanged(members) {
		cnf.Version++

		log.Info("Updating external nodes", "replset", rs.Name)

		err = cli.WriteConfig(ctx, cnf, false)
		if err != nil {
			return rsMembers, 0, errors.Wrap(err, "update external nodes: write mongo config")
		}
	}

	currMembers := append(mongo.ConfigMembers(nil), cnf.Members...)
	cnf.Members.SetVotes(members, unsafePSA)
	if !reflect.DeepEqual(currMembers, cnf.Members) {
		cnf.Version++

		log.Info("Configuring member votes and priorities", "replset", rs.Name)

		err := cli.WriteConfig(ctx, cnf, false)
		if err != nil {
			return rsMembers, 0, errors.Wrap(err, "set votes: write mongo config")
		}
	}

	rsStatus, err = cli.RSStatus(ctx)
	if err != nil {
		return rsMembers, 0, errors.Wrap(err, "unable to get replset members")
	}

	liveMembers := 0
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

		if podName, ok := tags["podName"]; ok {
			rsMembers[podName] = api.ReplsetMemberStatus{
				Name:     member.Name,
				State:    member.State,
				StateStr: member.StateStr,
			}
		}

		switch member.State {
		case mongo.MemberStatePrimary, mongo.MemberStateSecondary, mongo.MemberStateArbiter:
			liveMembers++
		}
	}

	return rsMembers, liveMembers, nil
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

		log.Info(resp.Msg,
			"shard", rsName,
			"note", resp.Note,
			"dbsToMove", resp.DBsToMove,
			"remaining dbs", resp.Remaining.DBs,
			"remaining chunks", resp.Remaining.Chunks,
			"remaining jumbo chunks", resp.Remaining.JumboChunks)

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

	host, err := psmdb.MongoHost(ctx, r.client, cr, cr.Spec.ClusterServiceDNSMode, replset, replset.Expose.Enabled, rspod)
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
func (r *ReconcilePerconaServerMongoDB) handleReplsetInit(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pods []corev1.Pod) (*corev1.Pod, *api.ReplsetMemberStatus, error) {
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

		member, err := r.getConfigMemberForPod(ctx, cr, replset, 0, &pod)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "get config member for pod %s", pod.Name)
		}

		memberBytes, err := json.Marshal(member)
		if err != nil {
			return nil, nil, errors.Wrap(err, "marshall member to json")
		}

		var errb, outb bytes.Buffer

		err = r.clientcmd.Exec(ctx, &pod, "mongod", []string{"mongod", "--version"}, nil, &outb, &errb, false)
		if err != nil {
			return nil, nil, fmt.Errorf("exec --version: %v / %s / %s", err, outb.String(), errb.String())
		}

		mongoCmd := "mongosh"
		if strings.Contains(outb.String(), "v4.4") || strings.Contains(outb.String(), "v5.0") {
			mongoCmd = "mongo"
		}

		if cr.TLSEnabled() {
			mongoCmd += " --tls --tlsCertificateKeyFile /tmp/tls.pem --tlsAllowInvalidCertificates --tlsCAFile /etc/mongodb-ssl/ca.crt"
		}

		mongoCmd += fmt.Sprintf(" --port %d", replset.GetPort())

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
							%s,
						]
					}
				)
				EOF
			`, mongoCmd, replsetName, memberBytes),
		}

		errb.Reset()
		outb.Reset()
		err = r.clientcmd.Exec(ctx, &pod, "mongod", cmd, nil, &outb, &errb, false)
		if err != nil {
			return nil, nil, fmt.Errorf("exec rs.initiate: %v / %s / %s", err, outb.String(), errb.String())
		}

		log.Info("replset initialized", "replset", replsetName, "pod", pod.Name)
		time.Sleep(time.Second * 5)

		log.Info("creating user admin", "replset", replsetName, "pod", pod.Name, "user", api.RoleUserAdmin)
		userAdmin, err := getInternalCredentials(ctx, r.client, cr, api.RoleUserAdmin)
		if err != nil {
			return nil, nil, errors.Wrap(err, "failed to get userAdmin credentials")
		}

		cmd[2] = fmt.Sprintf(`%s --eval %s`, mongoCmd, mongoInitAdminUser(userAdmin.Username, userAdmin.Password))
		errb.Reset()
		outb.Reset()
		err = r.clientcmd.Exec(ctx, &pod, "mongod", cmd, nil, &outb, &errb, false)
		if err != nil {
			return nil, nil, fmt.Errorf("exec add admin user: %v / %s / %s", err, outb.String(), errb.String())
		}
		log.Info("user admin created", "replset", replsetName, "pod", pod.Name, "user", api.RoleUserAdmin)

		return &pod, &api.ReplsetMemberStatus{
			Name:     member.Host,
			State:    mongo.MemberStatePrimary,
			StateStr: mongo.MemberStateStrings[mongo.MemberStatePrimary],
		}, nil
	}

	return nil, nil, errNoRunningMongodContainers
}

func (r *ReconcilePerconaServerMongoDB) handleReplicaSetNoPrimary(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pods []corev1.Pod) error {
	log := logf.FromContext(ctx).WithName("handleReplicaSetNoPrimary")

	for _, pod := range pods {
		if !isMongodPod(pod) || !isContainerAndPodRunning(pod, "mongod") || !isPodReady(pod) {
			continue
		}

		log.Info("Connecting to pod", "pod", pod.Name, "user", api.RoleClusterAdmin)
		cli, err := r.standaloneClientWithRole(ctx, cr, replset, api.RoleClusterAdmin, pod)
		if err != nil {
			return errors.Wrap(err, "get standalone mongo client")
		}

		cfg, err := cli.ReadConfig(ctx)
		if err != nil {
			return errors.Wrap(err, "read replset config")
		}

		if err := cli.WriteConfig(ctx, cfg, true); err != nil {
			return errors.Wrap(err, "reconfigure replset")
		}

		return nil
	}

	return errNoRunningMongodContainers
}

func getRoles(cr *api.PerconaServerMongoDB, role api.SystemUserRole) []mongo.Role {
	roles := make([]mongo.Role, 0)
	switch role {
	case api.RoleDatabaseAdmin:
		return []mongo.Role{
			{DB: "admin", Role: "readWriteAnyDatabase"},
			{DB: "admin", Role: "readAnyDatabase"},
			{DB: "admin", Role: "restore"},
			{DB: "admin", Role: "backup"},
			{DB: "admin", Role: "dbAdminAnyDatabase"},
			{DB: "admin", Role: string(api.RoleClusterMonitor)},
		}
	case api.RoleClusterMonitor:
		roles = []mongo.Role{
			{DB: "admin", Role: "explainRole"},
			{DB: "local", Role: "read"},
		}
		if cr.CompareVersion("1.20.0") >= 0 {
			roles = append(roles, mongo.Role{DB: "admin", Role: "directShardOperations"})
		}
	case api.RoleBackup:
		roles = []mongo.Role{
			{DB: "admin", Role: "readWrite"},
			{DB: "admin", Role: string(api.RoleClusterMonitor)},
			{DB: "admin", Role: "restore"},
			{DB: "admin", Role: "pbmAnyAction"},
		}
	case api.RoleClusterAdmin:
		if cr.CompareVersion("1.20.0") >= 0 {
			roles = []mongo.Role{
				{DB: "admin", Role: "directShardOperations"},
			}
		}
	}
	roles = append(roles, mongo.Role{DB: "admin", Role: string(role)})
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

// privilegesChanged compares 2 RolePrivilege arrays and returns true if they are not equal
func privilegesChanged(x []mongo.RolePrivilege, y []mongo.RolePrivilege) bool {
	if len(x) != len(y) {
		return true
	}
	for i := range x {
		if !compareResources(x[i].Resource, y[i].Resource) || !compareSlices(x[i].Actions, y[i].Actions) {
			return true
		}
	}
	return false
}

func compareTags(tags mongo.ReplsetTags, selector api.PrimaryPreferTagSelectorSpec) bool {
	if len(selector) == 0 {
		return false
	}
	for tag, v := range selector {
		if val, ok := tags[tag]; ok && val == v {
			continue
		}
		return false
	}
	return true
}

func (r *ReconcilePerconaServerMongoDB) createOrUpdateSystemRoles(ctx context.Context, cli mongo.Client, role string, privileges []mongo.RolePrivilege) error {
	roleInfo, err := cli.GetRole(ctx, "admin", role)
	if err != nil {
		return errors.Wrap(err, "mongo get role")
	}

	mo := mongo.Role{
		Role:       role,
		Privileges: privileges,
		Roles:      []mongo.InheritenceRole{},
	}

	if roleInfo == nil {
		err = cli.CreateRole(ctx, "admin", mo)
		return errors.Wrapf(err, "create role %s", role)
	}
	if privilegesChanged(privileges, roleInfo.Privileges) {
		err = cli.UpdateRole(ctx, "admin", mo)
		return errors.Wrapf(err, "update role")
	}
	return nil
}

// compareRoles compares 2 role arrays and returns true if they are equal
func compareRoles(x []mongo.Role, y []mongo.Role) bool {
	if len(x) != len(y) {
		return false
	}

	sortFunc := func(a mongo.Role, b mongo.Role) int {
		if a.DB < b.DB || a.Role < b.Role {
			return -1
		}

		return 0
	}

	slices.SortFunc(x, sortFunc)
	slices.SortFunc(y, sortFunc)

	for i := range x {
		if !reflect.DeepEqual(x[i], y[i]) {
			return false
		}
	}
	return true
}

func (r *ReconcilePerconaServerMongoDB) createOrUpdateSystemUsers(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) error {
	log := logf.FromContext(ctx)

	cli, err := r.mongoClientWithRole(ctx, cr, replset, api.RoleUserAdmin)
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

	users := []api.SystemUserRole{api.RoleClusterAdmin, api.RoleClusterMonitor, api.RoleBackup}
	if cr.CompareVersion("1.13.0") >= 0 {
		users = append(users, api.RoleDatabaseAdmin)
	}

	for _, role := range users {
		creds, err := getInternalCredentials(ctx, r.client, cr, role)
		if err != nil {
			log.Error(err, "failed to get credentials", "role", role)
			continue
		}
		user, err := cli.GetUserInfo(ctx, creds.Username, "admin")
		if err != nil {
			return errors.Wrap(err, "get user info")
		}
		if user == nil {
			log.Info("Creating user", "database", "admin", "user", creds.Username)
			err = cli.CreateUser(ctx, "admin", creds.Username, creds.Password, getRoles(cr, role)...)
			if err != nil {
				return errors.Wrapf(err, "failed to create user %s", role)
			}
			continue
		}
		if !compareRoles(user.Roles, getRoles(cr, role)) {
			log.Info("Updating user roles", "database", "admin", "user", creds.Username, "currentRoles", user.Roles, "newRoles", getRoles(cr, role))
			err = cli.UpdateUserRoles(ctx, "admin", creds.Username, getRoles(cr, role))
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
