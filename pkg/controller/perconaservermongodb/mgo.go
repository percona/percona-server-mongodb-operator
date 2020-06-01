package perconaservermongodb

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

var errReplsetLimit = fmt.Errorf("maximum replset member (%d) count reached", mongo.MaxMembers)

func (r *ReconcilePerconaServerMongoDB) reconcileCluster(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pods corev1.PodList, usersSecret *corev1.Secret) error {
	rsAddrs, err := psmdb.GetReplsetAddrs(r.client, cr, replset, pods.Items)
	if err != nil {
		return errors.Wrap(err, "get replset addr")
	}
	session, err := mongo.Dial(rsAddrs, replset.Name, usersSecret, true)
	if err != nil {
		session, err = mongo.Dial(rsAddrs, replset.Name, usersSecret, false)
		if err != nil {
			// try to init replset and if succseed
			// we'll go further on the next reconcile iteration
			if !cr.Status.Replsets[replset.Name].Initialized {
				err = r.handleReplsetInit(cr, replset, pods.Items)
				if err != nil {
					return errors.Wrap(err, "handleReplsetInit:")
				}
				cr.Status.Replsets[replset.Name].Initialized = true
				cr.Status.Conditions = append(cr.Status.Conditions, api.ClusterCondition{
					Status:             api.ConditionTrue,
					Type:               api.ClusterRSInit,
					Message:            replset.Name,
					LastTransitionTime: metav1.NewTime(time.Now()),
				})
				return nil
			}
			return errors.Wrap(err, "dial:")
		}
	}
	defer session.Disconnect(context.TODO())

	cnf, err := mongo.ReadConfig(context.TODO(), session)
	if err != nil {
		return errors.Wrap(err, "get mongo config")
	}

	members := mongo.RSMembers{}
	for key, pod := range pods.Items {
		if key >= mongo.MaxMembers {
			err = errReplsetLimit
			break
		}

		host, err := psmdb.MongoHost(r.client, cr, replset, pod)
		if err != nil {
			return fmt.Errorf("get host for pod %s: %v", pod.Name, err)
		}

		member := mongo.Member{
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
			return errors.Wrap(err, "delete: write mongo config")
		}
	}

	if cnf.Members.AddNew(members) {
		cnf.Members.RemoveOld(members)
		cnf.Members.SetVotes()

		cnf.Version++
		err = mongo.WriteConfig(context.TODO(), session, cnf)
		if err != nil {
			return errors.Wrap(err, "add new: write mongo config")
		}
	}

	return nil
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
