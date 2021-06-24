package perconaservermongodb

import (
	"bytes"
	"context"
	"fmt"
	"time"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	mgo "go.mongodb.org/mongo-driver/mongo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var errReplsetLimit = fmt.Errorf("maximum replset member (%d) count reached", mongo.MaxMembers)

func (r *ReconcilePerconaServerMongoDB) reconcileCluster(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec,
	pods corev1.PodList, mongosPods []corev1.Pod) (api.AppState, error) {

	if replset.Size == 0 {
		return api.AppStateReady, nil
	}

	// all pods needs to be scheduled to reconcile
	if int(replset.Size) > len(pods.Items) {
		return api.AppStateInit, nil
	}

	cli, err := r.mongoClientWithRole(cr, *replset, roleClusterAdmin)
	if err != nil {
		if cr.Status.Replsets[replset.Name].Initialized {
			return api.AppStateError, errors.Wrap(err, "dial:")
		}

		err := r.handleReplsetInit(cr, replset, pods.Items)
		if err != nil {
			return api.AppStateInit, errors.Wrap(err, "handleReplsetInit")
		}

		err = r.createSystemUsers(cr, replset)
		if err != nil {
			return api.AppStateInit, errors.Wrap(err, "create system users")
		}

		cr.Status.Replsets[replset.Name].Initialized = true
		cr.Status.AddCondition(api.ClusterCondition{
			Status:             api.ConditionTrue,
			Type:               api.AppStateInit,
			Message:            replset.Name,
			LastTransitionTime: metav1.NewTime(time.Now()),
		})

		return api.AppStateInit, nil
	}

	// this can happen if cluster is initialized but status update failed
	if !cr.Status.Replsets[replset.Name].Initialized {
		cr.Status.Replsets[replset.Name].Initialized = true
		cr.Status.AddCondition(api.ClusterCondition{
			Status:             api.ConditionTrue,
			Type:               api.AppStateInit,
			Message:            replset.Name,
			LastTransitionTime: metav1.NewTime(time.Now()),
		})

		return api.AppStateInit, nil
	}

	defer func() {
		err := cli.Disconnect(context.TODO())
		if err != nil {
			log.Error(err, "failed to close connection")
		}
	}()

	rstRunning, err := r.isRestoreRunning(cr)
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

		mongosSession, err := r.mongosClientWithRole(cr, roleClusterAdmin)
		if err != nil {
			return api.AppStateError, errors.Wrap(err, "failed to get mongos connection")
		}

		defer func() {
			err := mongosSession.Disconnect(context.TODO())
			if err != nil {
				log.Error(err, "failed to close mongos connection")
			}
		}()

		in, err := inShard(mongosSession, replset.Name)
		if err != nil {
			return api.AppStateError, errors.Wrap(err, "get shard")
		}

		if !in {
			log.Info("adding rs to shard", "rs", replset.Name)

			err := r.handleRsAddToShard(cr, replset, pods.Items[0], mongosPods[0])
			if err != nil {
				return api.AppStateError, errors.Wrap(err, "add shard")
			}

			log.Info("added to shard", "rs", replset.Name)
		}

		t := true
		cr.Status.Replsets[replset.Name].AddedAsShard = &t
	}

	cnf, err := mongo.ReadConfig(context.TODO(), cli)
	if err != nil {
		return api.AppStateError, errors.Wrap(err, "get mongo config")
	}

	members := mongo.ConfigMembers{}
	for key, pod := range pods.Items {
		if key >= mongo.MaxMembers {
			log.Error(errReplsetLimit, "rs", replset.Name)
			break
		}

		host, err := psmdb.MongoHost(r.client, cr, replset.Name, replset.Expose.Enabled, pod)
		if err != nil {
			return api.AppStateError, fmt.Errorf("get host for pod %s: %v", pod.Name, err)
		}

		member := mongo.ConfigMember{
			ID:           key,
			Host:         host,
			BuildIndexes: true,
		}

		switch pod.Labels["app.kubernetes.io/component"] {
		case "arbiter":
			member.ArbiterOnly = true
			member.Priority = 0
		case "mongod":
			member.Tags = mongo.ReplsetTags{
				"serviceName": cr.Name,
			}
		}

		members = append(members, member)
	}

	if cnf.Members.RemoveOld(members) {
		cnf.Members.SetVotes()

		cnf.Version++
		err = mongo.WriteConfig(context.TODO(), cli, cnf)
		if err != nil {
			return api.AppStateError, errors.Wrap(err, "delete: write mongo config")
		}
	}

	if cnf.Members.AddNew(members) {
		cnf.Members.RemoveOld(members)
		cnf.Members.SetVotes()

		cnf.Version++
		err = mongo.WriteConfig(context.TODO(), cli, cnf)
		if err != nil {
			return api.AppStateError, errors.Wrap(err, "add new: write mongo config")
		}
	}

	rsStatus, err := mongo.RSStatus(context.TODO(), cli)
	if err != nil {
		return api.AppStateError, errors.Wrap(err, "unable to get replset members")
	}

	membersLive := 0
	for _, member := range rsStatus.Members {
		switch member.State {
		case mongo.MemberStatePrimary, mongo.MemberStateSecondary, mongo.MemberStateArbiter:
			membersLive++
		case mongo.MemberStateStartup, mongo.MemberStateStartup2, mongo.MemberStateRecovering, mongo.MemberStateRollback:
			return api.AppStateInit, nil
		default:
			return api.AppStateError, errors.Errorf("undefined state of the replset member %s: %v", member.Name, member.State)
		}
	}

	if membersLive == len(pods.Items) {
		return api.AppStateReady, nil
	}

	return api.AppStateInit, nil
}

func inShard(client *mgo.Client, rsName string) (bool, error) {
	shardList, err := mongo.ListShard(context.TODO(), client)
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
	return fmt.Sprintf(`db.getSiblingDB("admin").createUser(
		{
			user: "%s",
			pwd: "%s",
			roles: [ "userAdminAnyDatabase" ] 
		}
	)`, user, pwd)
}

func (r *ReconcilePerconaServerMongoDB) removeRSFromShard(cr *api.PerconaServerMongoDB, rsName string) error {
	if !cr.Spec.Sharding.Enabled {
		return nil
	}

	cli, err := r.mongosClientWithRole(cr, roleClusterAdmin)
	if err != nil {
		return errors.Errorf("failed to get mongos connection: %v", err)
	}

	defer func() {
		err := cli.Disconnect(context.TODO())
		if err != nil {
			log.Error(err, "failed to close mongos connection")
		}
	}()

	for {
		resp, err := mongo.RemoveShard(context.Background(), cli, rsName)
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

func (r *ReconcilePerconaServerMongoDB) handleRsAddToShard(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, rspod corev1.Pod,
	mongosPod corev1.Pod) error {

	if !isContainerAndPodRunning(rspod, "mongod") || !isPodReady(rspod) {
		return errors.Errorf("rsPod %s is not ready", rspod.Name)
	}
	if !isContainerAndPodRunning(mongosPod, "mongos") || !isPodReady(mongosPod) {
		return errors.New("mongos pod is not ready")
	}

	host := psmdb.GetAddr(m, rspod.Name, replset.Name)

	cli, err := r.mongosClientWithRole(m, roleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "failed to get mongos client")
	}

	defer func() {
		err := cli.Disconnect(context.TODO())
		if err != nil {
			log.Error(err, "failed to close mongos connection")
		}
	}()

	err = mongo.AddShard(context.Background(), cli, replset.Name, host)
	if err != nil {
		return errors.Wrap(err, "failed to add shard")
	}

	return nil
}

// handleReplsetInit runs the k8s-mongodb-initiator from within the first running pod's mongod container.
// This must be ran from within the running container to utilize the MongoDB Localhost Exception.
//
// See: https://docs.mongodb.com/manual/core/security-users/#localhost-exception
//
func (r *ReconcilePerconaServerMongoDB) handleReplsetInit(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pods []corev1.Pod) error {
	for _, pod := range pods {
		if !isMongodPod(pod) || !isContainerAndPodRunning(pod, "mongod") || !isPodReady(pod) {
			continue
		}

		log.Info("initiating replset", "replset", replset.Name, "pod", pod.Name)

		host, err := psmdb.MongoHost(r.client, m, replset.Name, replset.Expose.Enabled, pod)
		if err != nil {
			return fmt.Errorf("get host for the pod %s: %v", pod.Name, err)
		}

		cmd := []string{
			"sh", "-c",
			fmt.Sprintf(
				`
				cat <<-EOF | mongo 
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
			`, replset.Name, host),
		}

		var errb, outb bytes.Buffer
		err = r.clientcmd.Exec(&pod, "mongod", cmd, nil, &outb, &errb, false)
		if err != nil {
			return fmt.Errorf("exec rs.initiate: %v / %s / %s", err, outb.String(), errb.String())
		}

		time.Sleep(time.Second * 5)

		userAdmin, err := r.getInternalCredentials(m, roleUserAdmin)
		if err != nil {
			return errors.Wrap(err, "failed to get userAdmin credentials")
		}

		cmd[2] = fmt.Sprintf(`
			cat <<-EOF | mongo 
			%s
			EOF`, mongoInitAdminUser(userAdmin.Username, userAdmin.Password))
		errb.Reset()
		outb.Reset()
		err = r.clientcmd.Exec(&pod, "mongod", cmd, nil, &outb, &errb, false)
		if err != nil {
			return fmt.Errorf("exec add admin user: %v / %s / %s", err, outb.String(), errb.String())
		}

		log.Info("replset was initialized", "replset", replset.Name, "pod", pod.Name)

		return nil
	}

	return errNoRunningMongodContainers
}

func (r *ReconcilePerconaServerMongoDB) createSystemUsers(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) error {
	cli, err := r.mongoClientWithRole(cr, *replset, roleUserAdmin)
	if err != nil {
		return errors.Wrap(err, "failed to get mongo client")
	}

	defer func() {
		err := cli.Disconnect(context.TODO())
		if err != nil {
			log.Error(err, "failed to close mongo connection")
		}
	}()

	clusterAdmin, err := r.getInternalCredentials(cr, roleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster admin")
	}

	err = mongo.CreateUser(context.Background(), cli, clusterAdmin.Username, clusterAdmin.Password, roleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "failed to create clusterAdmin")
	}

	monitorUser, err := r.getInternalCredentials(cr, roleClusterMonitor)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster admin")
	}

	err = mongo.CreateUser(context.Background(), cli, monitorUser.Username, monitorUser.Password, roleClusterMonitor)
	if err != nil {
		return errors.Wrap(err, "failed to create monitorUser")
	}

	err = mongo.CreateRole(context.Background(), cli, "pbmAnyAction",
		[]interface{}{
			map[string]interface{}{
				"resource": map[string]bool{"anyResource": true},
				"actions":  []string{"anyAction"},
			},
		}, []interface{}{})
	if err != nil {
		return errors.Wrap(err, "failed to create role")
	}

	backupUser, err := r.getInternalCredentials(cr, roleBackup)
	if err != nil {
		return errors.Wrap(err, "failed to get cluster admin")
	}

	err = mongo.CreateUser(context.Background(), cli, backupUser.Username, backupUser.Password,
		map[string]string{"db": "admin", "role": "readWrite", "collection": ""},
		map[string]string{"db": "admin", "role": string(roleBackup)},
		map[string]string{"db": "admin", "role": string(roleClusterMonitor)},
		map[string]string{"db": "admin", "role": "restore"},
		map[string]string{"db": "admin", "role": "pbmAnyAction"},
	)
	if err != nil {
		return errors.Wrap(err, "failed to create backup")
	}

	return nil
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
