package stub

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	podk8s "github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	watchdog "github.com/percona/mongodb-orchestration-tools/watchdog"
	wdConfig "github.com/percona/mongodb-orchestration-tools/watchdog/config"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// ReplsetInitWait is the duration to wait to initiate the replset
var ReplsetInitWait = 10 * time.Second

func NewHandler(serviceName, namespaceName, portName string) sdk.Handler {
	return &Handler{
		pods:         podk8s.NewPods(serviceName, namespaceName, portName),
		portName:     portName,
		serviceName:  serviceName,
		startedAt:    time.Now(),
		watchdogQuit: make(chan bool, 1),
	}
}

type Handler struct {
	pods         *podk8s.Pods
	portName     string
	serviceName  string
	watchdog     *watchdog.Watchdog
	watchdogQuit chan bool
	initialised  map[string]bool
	startedAt    time.Time
}

func (h *Handler) Close() {
	if h.watchdog != nil {
		h.watchdogQuit <- true
	}
}

func (h *Handler) isInitialised(replsetName string) bool {
	if _, ok := h.initialised[replsetName]; ok {
		return true
	}
	return false
}

func (h *Handler) Handle(ctx context.Context, event sdk.Event) error {
	switch o := event.Object.(type) {
	case *v1alpha1.PerconaServerMongoDB:
		psmdb := o

		// Ignore the delete event since the garbage collector will clean up all secondary resources for the CR
		// All secondary resources must have the CR set as their OwnerReference for this to be the case
		if event.Deleted {
			return nil
		}

		// Create the mongodb internal auth key if it doesn't exist
		authKey, err := newPSMDBMongoKeySecret(o)
		if err != nil {
			logrus.Errorf("failed to generate psmdb auth key: %v", err)
			return err
		}
		err = sdk.Create(authKey)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				logrus.Errorf("failed to create psmdb auth key: %v", err)
				return err
			}
		} else {
			logrus.Info("created mongodb auth key secret")
		}

		// load username/password from secret
		secret, err := getPSMDBSecret(psmdb, psmdb.Name+"-users")
		if err != nil {
			logrus.Errorf("failed to load psmdb user secrets: %v", err)
			return err
		}

		replsets := psmdb.Spec.MongoDB.Replsets
		if psmdb.Spec.MongoDB.Sharding {
			replsets = append(replsets, &v1alpha1.PerconaServerMongoDBReplset{
				Name:      configSvrReplsetName,
				Configsvr: true,
				Size:      int32(3),
			})
		}
		for _, replset := range replsets {
			// Create the StatefulSet if it doesn't exist
			set := newPSMDBStatefulSet(o, replset.Name)
			err = sdk.Create(set)
			if err != nil {
				if !errors.IsAlreadyExists(err) {
					logrus.Errorf("failed to create psmdb pod for replica set %s: %v", replset.Name, err)
					return err
				}
			} else {
				logrus.Infof("created mongodb stateful set for replica set %s", replset.Name)
			}

			// Ensure the stateful set size is the same as the spec
			err = sdk.Get(set)
			if err != nil {
				return fmt.Errorf("failed to get stateful set for replset %s: %v", replset.Name, err)
			}
			size := psmdb.Spec.Size
			if *set.Spec.Replicas != size {
				logrus.Infof("setting replicas to %d for replica set %s", size, replset.Name)
				set.Spec.Replicas = &size
				err = sdk.Update(set)
				if err != nil {
					return fmt.Errorf("failed to update stateful set for replica set %s: %v", replset.Name, err)
				}
			}

			// Initiate the replset if it hasn't already been initiated + there are pods +
			// we have waited the ReplsetInitWait period since starting
			//if !h.initialised && len(podList.Items) >= 1 && time.Since(h.startedAt) > ReplsetInitWait {
			if !h.isInitialised(replset.Name) && len(podList.Items) >= 1 {
				err = h.handleReplsetInit(psmdb, podList.Items, replset)
				if err != nil {
					logrus.Errorf("failed to init replset: %v", err)
				} else if h.watchdog == nil {
					// Start the watchdog if it has not been started
					h.watchdog = watchdog.New(&wdConfig.Config{
						ServiceName:    psmdb.Name,
						Username:       string(secret.Data["MONGODB_CLUSTER_ADMIN_USER"]),
						Password:       string(secret.Data["MONGODB_CLUSTER_ADMIN_PASSWORD"]),
						APIPoll:        5 * time.Second,
						ReplsetPoll:    5 * time.Second,
						ReplsetTimeout: 3 * time.Second,
					}, &h.watchdogQuit, h.pods)
					go h.watchdog.Run()
				}
			}
		}

		// Create the PSMDB service
		service := newPSMDBService(o)
		err = sdk.Create(service)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				logrus.Errorf("failed to create psmdb service: %v", err)
				return err
			}
		} else {
			logrus.Info("created mongodb service")
		}

		// Update the PerconaServerMongoDB status with the pod names and pod mongodb uri
		podList := podList()
		labelSelector := labels.SelectorFromSet(labelsForPerconaServerMongoDB(psmdb)).String()
		listOps := &metav1.ListOptions{LabelSelector: labelSelector}
		err = sdk.List(psmdb.Namespace, podList, sdk.WithListOptions(listOps))
		if err != nil {
			return fmt.Errorf("failed to list pods: %v", err)
		}
		podNames := getPodNames(podList.Items)
		if !reflect.DeepEqual(podNames, psmdb.Status.Nodes) {
			psmdb.Status.Nodes = podNames
			psmdb.Status.Uri = getMongoURI(podList.Items, h.portName)
			err := sdk.Update(psmdb)
			if err != nil {
				return fmt.Errorf("failed to update psmdb status: %v", err)
			}
		}

		// Update the pods list that is read by the watchdog
		h.pods.SetPods(podList.Items)
	}
	return nil
}

// labelsForPerconaServerMongoDB returns the labels for selecting the resources
// belonging to the given PerconaServerMongoDB CR name.
func labelsForPerconaServerMongoDB(m *v1alpha1.PerconaServerMongoDB) map[string]string {
	return map[string]string{
		"app":                       "percona-server-mongodb",
		"percona-server-mongodb_cr": m.Name,
	}
}

// addOwnerRefToObject appends the desired OwnerReference to the object
func addOwnerRefToObject(obj metav1.Object, ownerRef metav1.OwnerReference) {
	obj.SetOwnerReferences(append(obj.GetOwnerReferences(), ownerRef))
}

// asOwner returns an OwnerReference set as the PerconaServerMongoDB CR
func asOwner(m *v1alpha1.PerconaServerMongoDB) metav1.OwnerReference {
	trueVar := true
	return metav1.OwnerReference{
		APIVersion: m.APIVersion,
		Kind:       m.Kind,
		Name:       m.Name,
		UID:        m.UID,
		Controller: &trueVar,
	}
}

// podList returns a v1.PodList object
func podList() *corev1.PodList {
	return &corev1.PodList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
	}
}

// getPodNames returns the pod names of the array of pods passed in
func getPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}
