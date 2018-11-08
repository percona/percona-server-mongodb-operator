package stub

import (
	goErrors "errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	podk8s "github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	"github.com/sirupsen/logrus"
	mgo "gopkg.in/mgo.v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

var ErrNoRunningMongodContainers = goErrors.New("no mongod containers in running state")

// getRepsetMemberStatuses returns a list of ReplsetMemberStatus structs for a given replset
func getRepsetMemberStatuses(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret) []*v1alpha1.ReplsetMemberStatus {
	members := []*v1alpha1.ReplsetMemberStatus{}
	for _, pod := range pods {
		hostname := podk8s.GetMongoHost(pod.Name, m.Name, replset.Name, m.Namespace)
		dialInfo := &mgo.DialInfo{
			Addrs:          []string{hostname + ":" + strconv.Itoa(int(m.Spec.Mongod.Net.Port))},
			ReplicaSetName: replset.Name,
			Username:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminUser]),
			Password:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminPassword]),
			Direct:         true,
			FailFast:       true,
			Timeout:        time.Second,
		}
		logrus.Debugf("Updating status for host: %s", dialInfo.Addrs[0])

		session, err := mgo.DialWithInfo(dialInfo)
		if err != nil {
			logrus.Debugf("Cannot connect to mongodb host %s: %v", dialInfo.Addrs[0], err)
			continue
		}
		defer session.Close()
		session.SetMode(mgo.Eventual, true)

		buildInfo, err := session.BuildInfo()
		if err != nil {
			logrus.Debugf("Cannot get buildInfo from mongodb host %s: %v", dialInfo.Addrs[0], err)
			continue
		}

		members = append(members, &v1alpha1.ReplsetMemberStatus{
			Name:    dialInfo.Addrs[0],
			Version: buildInfo.Version,
		})
	}
	return members
}

// handleReplsetInit runs the k8s-mongodb-initiator from within the first running pod's mongod container.
// This must be ran from within the running container to utilise the MongoDB Localhost Exeception.
//
// See: https://docs.mongodb.com/manual/core/security-users/#localhost-exception
//
func (h *Handler) handleReplsetInit(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod) error {
	for _, pod := range pods {
		if !isMongodPod(pod) || !isContainerAndPodRunning(pod, mongodContainerName) || !isPodReady(pod) {
			continue
		}

		logrus.Infof("Initiating replset %s on running pod: %s", replset.Name, pod.Name)

		return execCommandInContainer(pod, mongodContainerName, []string{
			"/mongodb/k8s-mongodb-initiator",
			"init",
		})
	}
	return ErrNoRunningMongodContainers
}

// updateStatus updates the PerconaServerMongoDB status
func (h *Handler) updateStatus(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, usersSecret *corev1.Secret) (*corev1.PodList, error) {
	var doUpdate bool

	podList := podList()
	err := h.client.List(m.Namespace, podList, sdk.WithListOptions(getLabelSelectorListOpts(m, replset)))
	if err != nil {
		return nil, fmt.Errorf("failed to list pods for replset %s: %v", replset.Name, err)
	}

	// Update status pods list
	podNames := getPodNames(podList.Items)
	status := getReplsetStatus(m, replset)
	if !reflect.DeepEqual(podNames, status.Pods) {
		status.Pods = podNames
		doUpdate = true
	}

	// Update mongodb replset member status list
	members := getRepsetMemberStatuses(m, replset, podList.Items, usersSecret)
	if !reflect.DeepEqual(members, status.Members) {
		status.Members = members
		doUpdate = true
	}

	// Send update to SDK if something changed
	if doUpdate {
		err = h.client.Update(m)
		if err != nil {
			return nil, fmt.Errorf("failed to update status for replset %s: %v", replset.Name, err)
		}
	}

	// Update the pods list that is read by the watchdog
	if h.pods == nil {
		h.pods = podk8s.NewPods(m.Name, m.Namespace)
	}
	h.pods.SetPods(podList.Items)

	return podList, nil
}

// ensureReplsetStatefulSet ensures a StatefulSet exists
func (h *Handler) ensureReplsetStatefulSet(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) error {
	set, err := newPSMDBStatefulSet(m, replset, nil)
	if err != nil {
		return err
	}
	err = h.client.Create(set)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
	} else {
		logrus.WithFields(logrus.Fields{
			"size":          replset.Size,
			"limit_cpu":     m.Spec.Mongod.Limits.Cpu,
			"limit_memory":  m.Spec.Mongod.Limits.Memory,
			"limit_storage": m.Spec.Mongod.Limits.Storage,
		}).Infof("created stateful set for replset: %s", replset.Name)
	}

	// Ensure the stateful set size is the same as the spec
	err = h.client.Get(set)
	if err != nil {
		return fmt.Errorf("failed to get stateful set for replset %s: %v", replset.Name, err)
	}
	if *set.Spec.Replicas != replset.Size {
		logrus.Infof("setting replicas to %d for replset: %s", replset.Size, replset.Name)
		set.Spec.Replicas = &replset.Size
		err = h.client.Update(set)
		if err != nil {
			return fmt.Errorf("failed to update stateful set for replset %s: %v", replset.Name, err)
		}
	}

	return nil
}

// getReplsetStatus returns a ReplsetStatus object for a given replica set
func getReplsetStatus(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) *v1alpha1.ReplsetStatus {
	for _, rs := range m.Status.Replsets {
		if rs.Name == replset.Name {
			return rs
		}
	}
	status := &v1alpha1.ReplsetStatus{
		Name:    replset.Name,
		Members: []*v1alpha1.ReplsetMemberStatus{},
	}
	m.Status.Replsets = append(m.Status.Replsets, status)
	return status
}

// statusHasPod returns a boolean reflecting if a ReplsetSTatus contains a
// pod name
func statusHasPod(status *v1alpha1.ReplsetStatus, podName string) bool {
	for _, pod := range status.Pods {
		if pod == podName {
			return true
		}
	}
	return false
}

// ensureReplset ensures resources for a PSMDB replset exist
func (h *Handler) ensureReplset(m *v1alpha1.PerconaServerMongoDB, podList *corev1.PodList, replset *v1alpha1.ReplsetSpec, usersSecret *corev1.Secret) (*v1alpha1.ReplsetStatus, error) {
	// Create the StatefulSet if it doesn't exist
	err := h.ensureReplsetStatefulSet(m, replset)
	if err != nil {
		logrus.Errorf("failed to create stateful set for replset %s: %v", replset.Name, err)
		return nil, err
	}

	// Initiate the replset if it hasn't already been initiated + there are pods +
	// we have waited the ReplsetInitWait period since starting
	status := getReplsetStatus(m, replset)
	if !status.Initialised && len(podList.Items) >= 1 && time.Since(h.startedAt) > ReplsetInitWait {
		err = h.handleReplsetInit(m, replset, podList.Items)
		if err != nil {
			return nil, err
		}

		// update status after replset init
		status.Initialised = true
		err = h.client.Update(m)
		if err != nil {
			return nil, fmt.Errorf("failed to update status for replset %s: %v", replset.Name, err)
		}
		logrus.Infof("changed state to initialised for replset %s", replset.Name)

		// ensure the watchdog is started
		err = h.ensureWatchdog(m, usersSecret)
		if err != nil {
			return nil, fmt.Errorf("failed to start watchdog: %v", err)
		}
	}

	// Create service for replset
	service := newPSMDBService(m, replset)
	err = h.client.Create(service)
	if err != nil {
		if !errors.IsAlreadyExists(err) {
			logrus.Errorf("failed to create psmdb service: %v", err)
			return nil, err
		}
	} else {
		logrus.Infof("created service %s", service.Name)
	}

	return status, nil
}
