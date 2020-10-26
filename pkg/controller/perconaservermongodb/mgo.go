package perconaservermongodb

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"regexp"
	"strings"
	"time"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	mgo "go.mongodb.org/mongo-driver/mongo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	envMongoDBClusterAdminUser       = "MONGODB_CLUSTER_ADMIN_USER"
	envMongoDBClusterAdminPassword   = "MONGODB_CLUSTER_ADMIN_PASSWORD"
	envMongoDBUserAdminUser          = "MONGODB_USER_ADMIN_USER"
	envMongoDBUserAdminPassword      = "MONGODB_USER_ADMIN_PASSWORD"
	envMongoDBBackupUser             = "MONGODB_BACKUP_USER"
	envMongoDBBackupPassword         = "MONGODB_BACKUP_PASSWORD"
	envMongoDBClusterMonitorUser     = "MONGODB_CLUSTER_MONITOR_USER"
	envMongoDBClusterMonitorPassword = "MONGODB_CLUSTER_MONITOR_PASSWORD"
	envPMMServerUser                 = "PMM_SERVER_USER"
	envPMMServerPassword             = "PMM_SERVER_PASSWORD"
)

var errReplsetLimit = fmt.Errorf("maximum replset member (%d) count reached", mongo.MaxMembers)

func (r *ReconcilePerconaServerMongoDB) reconcileCluster(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec,
	pods corev1.PodList, usersSecret *corev1.Secret, mongosPods []corev1.Pod) (mongoClusterState, error) {
	username := string(usersSecret.Data[envMongoDBClusterAdminUser])
	password := string(usersSecret.Data[envMongoDBClusterAdminPassword])
	session, err := r.mongoClient(cr, replset, pods, username, password)
	if err != nil {
		// try to init replset and if succseed
		// we'll go further on the next reconcile iteration
		if !cr.Status.Replsets[replset.Name].Initialized {
			err = r.handleReplsetInit(cr, replset, pods.Items)
			if err != nil {
				return clusterInit, errors.Wrap(err, "handleReplsetInit:")
			}
			cr.Status.Replsets[replset.Name].Initialized = true
			cr.Status.Conditions = append(cr.Status.Conditions, api.ClusterCondition{
				Status:             api.ConditionTrue,
				Type:               api.ClusterRSInit,
				Message:            replset.Name,
				LastTransitionTime: metav1.NewTime(time.Now()),
			})
			return clusterInit, nil
		}
		return clusterError, errors.Wrap(err, "dial:")
	}

	defer func() {
		err := session.Disconnect(context.TODO())
		if err != nil {
			log.Error(err, "failed to close connection")
		}
	}()

	if cr.Status.Replsets[replset.Name].Initialized &&
		cr.Status.Replsets[replset.Name].Status == api.AppStateReady &&
		replset.ClusterRole == api.ClusterRoleShardSvr {

		conf := mongo.Config{
			Hosts:    []string{cr.Name + "-" + "mongos"},
			Username: username,
			Password: password,
		}

		mongosSession, err := mongo.Dial(&conf)
		if err != nil {
			return clusterError, errors.Wrap(err, "failed to connect to mongos")
		}

		defer func() {
			err := mongosSession.Disconnect(context.TODO())
			if err != nil {
				log.Error(err, "failed to close mongos connection")
			}
		}()

		in, err := inShard(mongosSession, pods.Items[0].Name)
		if err != nil {
			return clusterError, errors.Wrap(err, "add shard")
		}

		if !in {
			log.Info("adding rs to shard", "rs", replset.Name)

			err := r.handleRsAddToShard(cr, replset, pods.Items[0], mongosPods)
			if err != nil {
				return clusterError, errors.Wrap(err, "add shard")
			}

			log.Info("added to shard", "rs", replset.Name)

			cr.Status.Replsets[replset.Name].AddedAsShard = true
		}
	}

	cnf, err := mongo.ReadConfig(context.TODO(), session)
	if err != nil {
		return clusterError, errors.Wrap(err, "get mongo config")
	}

	members := mongo.ConfigMembers{}
	for key, pod := range pods.Items {
		if key >= mongo.MaxMembers {
			err = errReplsetLimit
			break
		}

		host, err := psmdb.MongoHost(r.client, cr, replset, pod)
		if err != nil {
			return clusterError, fmt.Errorf("get host for pod %s: %v", pod.Name, err)
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
		err = mongo.WriteConfig(context.TODO(), session, cnf)
		if err != nil {
			return clusterError, errors.Wrap(err, "delete: write mongo config")
		}
	}

	if cnf.Members.AddNew(members) {
		cnf.Members.RemoveOld(members)
		cnf.Members.SetVotes()

		cnf.Version++
		err = mongo.WriteConfig(context.TODO(), session, cnf)
		if err != nil {
			return clusterError, errors.Wrap(err, "add new: write mongo config")
		}
	}

	rsStatus, err := mongo.RSStatus(context.TODO(), session)
	if err != nil {
		return clusterError, errors.Wrap(err, "unable to get replset members")
	}
	membersLive := 0
	for _, member := range rsStatus.Members {
		switch member.State {
		case mongo.MemberStatePrimary, mongo.MemberStateSecondary, mongo.MemberStateArbiter:
			membersLive++
		case mongo.MemberStateStartup, mongo.MemberStateStartup2, mongo.MemberStateRecovering, mongo.MemberStateRollback:
			return clusterInit, nil
		default:
			return clusterError, errors.Errorf("undefined state of the replset member %s: %v", member.Name, member.State)
		}
	}
	if membersLive == len(pods.Items) {
		return clusterReady, nil
	}
	return clusterInit, nil
}

func inShard(client *mgo.Client, podName string) (bool, error) {
	shardList, err := mongo.ListShard(context.TODO(), client)
	if err != nil {
		return false, errors.Wrap(err, "unable to get shard list")
	}

	for _, shard := range shardList.Shards {
		if strings.Contains(shard.Host, podName) {
			return true, nil
		}
	}

	return false, nil
}

func (r *ReconcilePerconaServerMongoDB) mongoClient(cr *api.PerconaServerMongoDB, replSet *api.ReplsetSpec, pods corev1.PodList,
	username, password string) (*mgo.Client, error) {
	rsAddrs, err := psmdb.GetReplsetAddrs(r.client, cr, replSet, pods.Items)
	if err != nil {
		return nil, errors.Wrap(err, "get replset addr")
	}

	conf := &mongo.Config{
		ReplSetName: replSet.Name,
		Hosts:       rsAddrs,
		Username:    username,
		Password:    password,
	}

	if !cr.Spec.UnsafeConf {
		certSecret := &corev1.Secret{}
		err := r.client.Get(context.TODO(), types.NamespacedName{
			Name:      cr.Spec.Secrets.SSL,
			Namespace: cr.Namespace,
		}, certSecret)
		if err != nil {
			return nil, errors.Wrap(err, "get ssl certSecret")
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(certSecret.Data["ca.crt"])

		var clientCerts []tls.Certificate
		cert, err := tls.X509KeyPair(certSecret.Data["tls.crt"], certSecret.Data["tls.key"])
		if err != nil {
			return nil, errors.Wrap(err, "load keypair")
		}
		clientCerts = append(clientCerts, cert)
		conf.TLSConf = &tls.Config{
			InsecureSkipVerify: true,
			RootCAs:            pool,
			Certificates:       clientCerts,
		}
	}

	return mongo.Dial(conf)
}

var errNoRunningMongodContainers = errors.New("no mongod containers in running state")

const (
	mongoInitAdminUser = `
	db.getSiblingDB("admin").createUser(
		{
			user: "${MONGODB_USER_ADMIN_USER}",
			pwd: "${MONGODB_USER_ADMIN_PASSWORD}",
			roles: [ "userAdminAnyDatabase" ] 
		}
	)
	`

	mongoInitUsers = `
	db.getSiblingDB("admin").createUser(
		{
			user: "${MONGODB_CLUSTER_ADMIN_USER}",
			pwd: "${MONGODB_CLUSTER_ADMIN_PASSWORD}",
			roles: [ "clusterAdmin" ] 
		}
	)
	db.getSiblingDB("admin").createUser(
		{
			user: "${MONGODB_CLUSTER_MONITOR_USER}",
			pwd: "${MONGODB_CLUSTER_MONITOR_PASSWORD}",
			roles: [ "clusterMonitor" ] 
		}
	)
	
	db.getSiblingDB("admin").createRole({ "role": "pbmAnyAction",
		  "privileges": [
			 { "resource": { "anyResource": true },
			   "actions": [ "anyAction" ]
			 }
		  ],
		  "roles": []
	   });
	db.getSiblingDB("admin").createUser(
		{
			user: "${MONGODB_BACKUP_USER}",
			pwd: "${MONGODB_BACKUP_PASSWORD}",
			"roles" : [
				{ "db" : "admin", "role" : "readWrite", "collection": "" },
				{ "db" : "admin", "role" : "backup" },
				{ "db" : "admin", "role" : "clusterMonitor" },
				{ "db" : "admin", "role" : "restore" },
				{ "db" : "admin", "role" : "pbmAnyAction" }
			 ]
		}
	)
	`
)

func (r *ReconcilePerconaServerMongoDB) handleRsAddToShard(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, rspod corev1.Pod,
	mongosPods []corev1.Pod) error {
	if len(mongosPods) != int(m.Spec.Sharding.Mongos.Size) {
		return errors.New("not all mongos pods run")
	}

	var re = regexp.MustCompile(`(?m)"ok"\s*:\s*1,`)
	for _, pod := range mongosPods {
		if !isContainerAndPodRunning(rspod, "mongod") || !isPodReady(rspod) {
			return errors.New("rsPod is not redy")
		}
		if !isContainerAndPodRunning(pod, "mongos") || !isPodReady(pod) {
			return errors.New("mongos pod is not ready")
		}

		host := psmdb.GetAddr(m, rspod.Name, replset.Name)

		cmd := []string{
			"sh", "-c",
			fmt.Sprintf(
				`
				cat <<-EOF | mongo "mongodb://${MONGODB_CLUSTER_ADMIN_USER}:${MONGODB_CLUSTER_ADMIN_PASSWORD}@localhost"
				sh.addShard("%s/%s")
				EOF
			`, replset.Name, host),
		}

		var errb, outb bytes.Buffer
		err := r.clientcmd.Exec(&pod, "mongos", cmd, nil, &outb, &errb, false)
		if err != nil {
			return fmt.Errorf("exec sh.addShard: %v / %s / %s", err, outb.String(), errb.String())
		}

		if !re.Match(outb.Bytes()) {
			return errors.New("failed to add shard")
		}
	}

	return nil
}

// handleReplsetInit runs the k8s-mongodb-initiator from within the first running pod's mongod container.
// This must be ran from within the running container to utilise the MongoDB Localhost Exeception.
//
// See: https://docs.mongodb.com/manual/core/security-users/#localhost-exception
//
func (r *ReconcilePerconaServerMongoDB) handleReplsetInit(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pods []corev1.Pod) error {
	for _, pod := range pods {
		if !isMongodPod(pod) || !isContainerAndPodRunning(pod, "mongod") || !isPodReady(pod) {
			continue
		}

		log.Info("Initiating replset", "replset", replset.Name, "pod", pod.Name)

		host, err := psmdb.MongoHost(r.client, m, replset, pod)
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

		cmd[2] = fmt.Sprintf(
			`
			cat <<-EOF | mongo 
			%s
			EOF
			`, mongoInitAdminUser)
		errb.Reset()
		outb.Reset()
		err = r.clientcmd.Exec(&pod, "mongod", cmd, nil, &outb, &errb, false)
		if err != nil {
			return fmt.Errorf("exec add admin user: %v / %s / %s", err, outb.String(), errb.String())
		}

		cmd[2] = fmt.Sprintf(
			`
			cat <<-EOF | mongo "mongodb://${MONGODB_USER_ADMIN_USER}:${MONGODB_USER_ADMIN_PASSWORD}@%s/?replicaSet=%s"
			%s
			EOF
			`, host, replset.Name, mongoInitUsers)
		errb.Reset()
		outb.Reset()
		err = r.clientcmd.Exec(&pod, "mongod", cmd, nil, &outb, &errb, false)
		if err != nil {
			return fmt.Errorf("exec add users: %v / %s / %s", err, outb.String(), errb.String())
		}

		return nil
	}
	return errNoRunningMongodContainers
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
