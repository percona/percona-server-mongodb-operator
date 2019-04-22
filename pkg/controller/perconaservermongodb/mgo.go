package perconaservermongodb

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

var errReplsetLimit = fmt.Errorf("maximum replset member (%d) count reached", mongo.MaxMembers)

func (r *ReconcilePerconaServerMongoDB) reconcileCluster(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pods corev1.PodList, usersSecret *corev1.Secret) error {
	session, err := mongo.Dial(r.getReplsetAddrs(cr, replset, pods.Items), replset.Name, usersSecret)
	if err != nil {
		// try to init replset and if succseed
		// we'll go further on the next reconcile iteration
		if !cr.Status.Replsets[replset.Name].Initialized {
			err = r.handleReplsetInit(cr, replset, pods.Items)
			if err != nil {
				return errors.Wrap(err, "handleReplsetInit:")
			}
			cr.Status.Replsets[replset.Name].Initialized = true
			return nil
		}
		return errors.Wrap(err, "dial:")
	}
	defer session.Close()

	cnf, err := mongo.ReadConfig(session)
	if err != nil {
		return errors.Wrap(err, "get mongo config")
	}

	members := mongo.RSMembers{}
	for key, pod := range pods.Items {
		if key >= mongo.MaxMembers {
			err = errReplsetLimit
			break
		}

		host, err := r.mongoHost(cr, replset, pod)
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
		err = mongo.WriteConfig(session, cnf)
		if err != nil {
			return errors.Wrap(err, "delete: write mongo config")
		}
	}

	if cnf.Members.AddNew(members) {
		cnf.Members.RemoveOld(members)
		cnf.Members.SetVotes()

		cnf.Version++
		err = mongo.WriteConfig(session, cnf)
		if err != nil {
			return errors.Wrap(err, "add new: write mongo config")
		}
	}

	return nil
}

// getReplsetAddrs returns a slice of replset host:port addresses
func (r *ReconcilePerconaServerMongoDB) getReplsetAddrs(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pods []corev1.Pod) []string {
	addrs := make([]string, 0)

	for _, pod := range pods {
		host, err := r.mongoHost(m, replset, pod)
		if err != nil {
			log.Error(err, "failed to get external hostname")
			continue
		}
		addrs = append(addrs, host)
	}

	return addrs
}

func (r *ReconcilePerconaServerMongoDB) mongoHost(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pod corev1.Pod) (string, error) {
	if replset.Expose.Enabled {
		return r.getExtAddr(m.Namespace, pod)
	}

	return getAddr(m, pod.Name, replset.Name), nil
}

func (r *ReconcilePerconaServerMongoDB) getExtAddr(namespace string, pod corev1.Pod) (string, error) {
	svc, err := r.getExtServices(namespace, pod.Name)
	if err != nil {
		return "", fmt.Errorf("fetch service address: %v", err)
	}

	hostname, err := psmdb.GetServiceAddr(*svc, pod, r.client)
	if err != nil {
		return "", fmt.Errorf("get service hostname: %v", err)
	}

	return hostname.String(), nil
}

func getAddr(m *api.PerconaServerMongoDB, pod, replset string) string {
	return strings.Join([]string{pod, m.Name + "-" + replset}, ".") +
		":" + strconv.Itoa(int(m.Spec.Mongod.Net.Port))
}

var ErrNoRunningMongodContainers = fmt.Errorf("no mongod containers in running state")

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

		cmd := []string{
			"k8s-mongodb-initiator",
			"init",
		}

		if replset.Expose.Enabled {
			svc, err := r.getExtServices(m.Namespace, pod.Name)
			if err != nil {
				return fmt.Errorf("failed to fetch services: %v", err)
			}
			hostname, err := psmdb.GetServiceAddr(*svc, pod, r.client)
			if err != nil {
				return fmt.Errorf("failed to fetch service address: %v", err)
			}
			cmd = append(cmd, "--ip", hostname.Host, "--port", strconv.Itoa(hostname.Port))
		}

		var errb bytes.Buffer
		err := r.clientcmd.Exec(&pod, "mongod", cmd, nil, nil, &errb, false)
		if err != nil {
			return fmt.Errorf("exec: %v /  %s", err, errb.String())
		}

		return nil
	}
	return ErrNoRunningMongodContainers
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

func (r *ReconcilePerconaServerMongoDB) getExtServices(namespace, podName string) (*corev1.Service, error) {
	var retries uint64 = 0

	svcMeta := &corev1.Service{}

	for retries <= 5 {
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: podName, Namespace: namespace}, svcMeta)

		if err != nil {
			if k8serrors.IsNotFound(err) {
				retries += 1
				time.Sleep(500 * time.Millisecond)
				log.Info("Service for %s not found. Retry", podName)
				continue
			}
			return nil, fmt.Errorf("failed to fetch service: %v", err)
		}
		return svcMeta, nil
	}
	return nil, fmt.Errorf("failed to fetch service. Retries limit reached")
}
