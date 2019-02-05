package stub

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/mongod"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	podk8s "github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	"github.com/sirupsen/logrus"
	"gopkg.in/mgo.v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

var (
	ErrNoRunningMongodContainers = errors.New("no mongod containers in running state")
	MongoDBTimeout               = 3 * time.Second
)

// GetReplsetAddrs returns a slice of replset host:port addresses
func GetReplsetAddrs(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod) []string {
	addrs := make([]string, 0)
	var hostname string

	if replset.Expose != nil && replset.Expose.Enabled {
		for _, pod := range pods {
			hostname, err := getSvcAddr(m, pod)
			if err != nil {
				logrus.Errorf("failed to get service hostname: %v", err)
				continue
			}
			addrs = append(addrs, hostname.String())
		}
	} else {
		for _, pod := range pods {
			hostname = podk8s.GetMongoHost(pod.Name, m.Name, replset.Name, m.Namespace)
			addrs = append(addrs, hostname+":"+strconv.Itoa(int(m.Spec.Mongod.Net.Port)))
		}
	}
	return addrs
}

// getReplsetDialInfo returns a *mgo.Session configured to connect (with auth) to a Pod MongoDB
func getReplsetDialInfo(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret) *mgo.DialInfo {
	return &mgo.DialInfo{
		Addrs:          GetReplsetAddrs(m, replset, pods),
		ReplicaSetName: replset.Name,
		Username:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminUser]),
		Password:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminPassword]),
		Timeout:        MongoDBTimeout,
		FailFast:       true,
	}
}

// isReplsetInitialized returns a boolean reflecting if the replica set has been initiated
func isReplsetInitialized(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, status *v1alpha1.ReplsetStatus, podList *corev1.PodList, usersSecret *corev1.Secret) bool {
	if status.Initialized {
		return true
	}

	// try making a replica set connection to the pods to
	// check if the replset was already initialized
	session, err := mgo.DialWithInfo(getReplsetDialInfo(m, replset, podList.Items, usersSecret))
	if err != nil {
		logrus.Debugf("Cannot connect to mongodb replset %s to check initialization: %v", replset.Name, err)
		return false
	}
	defer session.Close()
	err = session.Ping()
	if err != nil {
		logrus.Debugf("Cannot ping mongodb replset %s to check initialization: %v", replset.Name, err)
		return false
	}

	// if we made it here the replset was already initialized
	status.Initialized = true

	return true
}

// handleReplsetInit runs the k8s-mongodb-initiator from within the first running pod's mongod container.
// This must be ran from within the running container to utilise the MongoDB Localhost Exeception.
//
// See: https://docs.mongodb.com/manual/core/security-users/#localhost-exception
//
func (h *Handler) handleReplsetInit(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod) error {
	for _, pod := range pods {
		if !isMongodPod(pod) || !util.IsContainerAndPodRunning(pod, mongod.MongodContainerName) || !util.IsPodReady(pod) {
			continue
		}

		logrus.Infof("Initiating replset %s on running pod: %s", replset.Name, pod.Name)

		cmd := []string{
			"k8s-mongodb-initiator",
			"init",
		}

		if replset.Expose != nil && replset.Expose.Enabled {
			hostname, err := getSvcAddr(m, pod)
			if err != nil {
				return fmt.Errorf("failed to fetch service address: %v", err)
			}
			cmd = append(cmd, "--ip", hostname.Host, "--port", strconv.Itoa(hostname.Port))

		}
		return execCommandInContainer(pod, mongod.MongodContainerName, cmd)
	}
	return ErrNoRunningMongodContainers
}

func (h *Handler) handleStatefulSetUpdate(m *v1alpha1.PerconaServerMongoDB, set *appsv1.StatefulSet, replset *v1alpha1.ReplsetSpec, resources corev1.ResourceRequirements) (*appsv1.StatefulSet, error) {
	// Ensure the stateful set size is the same as the spec
	if *set.Spec.Replicas != replset.Size {
		logrus.Infof("setting replicas to %d for replset: %s", replset.Size, replset.Name)
		set.Spec.Replicas = &replset.Size
	}

	// Ensure the stateful set containers are the same as the spec
	//
	// TODO: only send an update if something changed. reflect.DeepEqual
	// considers two struct to be different always if they contain pointers.
	// For now we send an update every loop like the PXC operator
	set.Spec.Template.Spec.Containers = h.newStatefulSetContainers(m, replset, resources)
	err := h.client.Update(set)
	if err != nil {
		return nil, fmt.Errorf("failed to update stateful set for replset %s: %v", replset.Name, err)
	}
	return set, nil
}

// ensureReplsetStatefulSet ensures a StatefulSet exists
func (h *Handler) ensureReplsetStatefulSet(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, resources corev1.ResourceRequirements) (*appsv1.StatefulSet, error) {
	// Check if 'resources.limits.storage' is unset
	// https://jira.percona.com/browse/CLOUD-42
	if _, ok := resources.Limits[corev1.ResourceStorage]; !ok {
		return nil, fmt.Errorf("replset %s does not have required-value 'resources.limits.storage' set", replset.Name)
	}

	// create the statefulset if a Get on the set name returns an error
	set := util.NewStatefulSet(m, m.Name+"-"+replset.Name)
	err := h.client.Get(set)
	if err != nil {

		lf := logrus.Fields{
			"version": m.Spec.Version,
			"size":    replset.Size,
			"cpu":     replset.Limits.Cpu,
			"memory":  replset.Limits.Memory,
			"storage": replset.Limits.Storage,
		}
		if replset.StorageClass != "" {
			lf["storageClass"] = replset.StorageClass
		}

		set, err = h.newStatefulSet(m, replset, resources)
		if err != nil {
			return nil, err
		}
		err = h.client.Create(set)
		if err != nil {
			if !k8serrors.IsAlreadyExists(err) {
				return nil, err
			}
		} else {
			logrus.WithFields(lf).Infof("created stateful set for replset: %s", replset.Name)
		}
	}

	// Ensure the spec is up to date
	return h.handleStatefulSetUpdate(m, set, replset, resources)
}

// ensureReplset ensures resources for a PSMDB replset exist
func (h *Handler) ensureReplset(m *v1alpha1.PerconaServerMongoDB, podList *corev1.PodList, replset *v1alpha1.ReplsetSpec, usersSecret *corev1.Secret) (map[string]*appsv1.StatefulSet, error) {
	status := getReplsetStatus(m, replset)

	resources, err := util.ParseResourceSpecRequirements(replset.Limits, replset.Requests)
	if err != nil {
		return nil, err
	}

	sets := make(map[string]*appsv1.StatefulSet, 2)

	// Create the StatefulSet if it doesn't exist
	set, err := h.ensureReplsetStatefulSet(m, replset, resources)
	if err != nil {
		return nil, err
	}
	sets["mongod"] = set

	// Create arbiter if it enabled in cr definitions
	if replset.Arbiter != nil && replset.Arbiter.Enabled && replset.Arbiter.Size >= 1 {
		arbiter, err := h.ensureReplsetArbiter(m, replset, resources)
		if err != nil {
			return nil, err
		}
		sets["arbiter"] = arbiter
	}

	// Initiate the replset if it hasn't already been initiated + there are pods +
	// we have waited the ReplsetInitWait period since starting
	if !isReplsetInitialized(m, replset, status, podList, usersSecret) && len(podList.Items) >= 1 && time.Since(h.startedAt) > ReplsetInitWait {
		err = h.handleReplsetInit(m, replset, podList.Items)
		if err != nil {
			return nil, err
		}

		err = h.client.Get(m)
		if err != nil {
			return nil, fmt.Errorf("failed to get status for replset %s: %v", replset.Name, err)
		}
		status.Initialized = true

		err = h.client.Update(m)
		if err != nil {
			return nil, fmt.Errorf("failed to update status for replset %s: %v", replset.Name, err)
		}

		logrus.Infof("changed state to initialised for replset %s", replset.Name)
	}

	// Remove PVCs left-behind from scaling down if no update is running
	if !util.IsStatefulSetUpdating(set) {
		err = h.persistentVolumeClaimReaper(m, podList.Items, replset, status)
		if err != nil {
			logrus.Errorf("failed to run persistent volume claim reaper for replset %s: %v", replset.Name, err)
			return nil, err
		}
	}

	if replset.Expose == nil || !replset.Expose.Enabled {
		// Create service for replset
		service := newService(m, replset)
		err = h.client.Create(service)
		if err != nil {
			if !k8serrors.IsAlreadyExists(err) {
				logrus.Errorf("failed to create psmdb service: %v", err)
				return nil, err
			}
		} else {
			logrus.Infof("created service %s", service.Name)
		}
	}

	return sets, nil
}
