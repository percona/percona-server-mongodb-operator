package stub

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
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

func NewHandler(portName string) sdk.Handler {
	return &Handler{
		pods:         podk8s.NewPods([]corev1.Pod{}, "mongodb"),
		portName:     portName,
		watchdogQuit: make(chan bool, 1),
	}
}

type Handler struct {
	pods         *podk8s.Pods
	portName     string
	watchdog     *watchdog.Watchdog
	watchdogQuit chan bool
	initialised  bool
}

func (h *Handler) Close() {
	if h.watchdog != nil {
		h.watchdogQuit <- true
	}
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

		// Start the watchdog if it has not been started
		if h.watchdog == nil {
			h.watchdog = watchdog.New(&wdConfig.Config{
				ServiceName:    psmdb.Namespace,
				APIPoll:        5 * time.Second,
				ReplsetPoll:    5 * time.Second,
				ReplsetTimeout: 10 * time.Second,
			}, &h.watchdogQuit, h.pods)
			go h.watchdog.Run()
		}

		// Create the StatefulSet if it doesn't exist
		set := newPSMDBStatefulSet(o)
		err := sdk.Create(set)
		if err != nil && !errors.IsAlreadyExists(err) {
			logrus.Errorf("failed to create psmdb pod : %v", err)
			return err
		}

		// Ensure the stateful set size is the same as the spec
		err = sdk.Get(set)
		if err != nil {
			return fmt.Errorf("failed to get stateful set: %v", err)
		}
		size := psmdb.Spec.Size
		if *set.Spec.Replicas != size {
			set.Spec.Replicas = &size
			err = sdk.Update(set)
			if err != nil {
				return fmt.Errorf("failed to update stateful set: %v", err)
			}
		}

		// Update the PerconaServerMongoDB status with the pod names and pod mongodb uri
		podList := podList()
		labelSelector := labels.SelectorFromSet(labelsForPerconaServerMongoDB(psmdb.Name)).String()
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

		// Initiate the replset
		if !h.initialised && len(podList.Items) >= 1 {
			logrus.Info("Initiating replset")

			job := newPSMDBReplsetInitJob(o)
			err = sdk.Create(job)
			if err != nil && !errors.IsAlreadyExists(err) {
				logrus.Errorf("failed to create psmdb replset init job: %v", err)
				return err
			}

			err = sdk.Get(job)
			if err != nil {
				return fmt.Errorf("failed to get psmdb replset init job: %v", err)
			}

			h.initialised = true
		}
	}
	return nil
}

// labelsForPerconaServerMongoDB returns the labels for selecting the resources
// belonging to the given PerconaServerMongoDB CR name.
func labelsForPerconaServerMongoDB(name string) map[string]string {
	return map[string]string{
		"app":                       "percona-server-mongodb",
		"percona-server-mongodb_cr": name,
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

// getMongoURI returns the mongodb uri containing the host/port of each pod
func getMongoURI(pods []corev1.Pod, portName string) string {
	var hosts []string
	for _, pod := range pods {
		if pod.Status.HostIP == "" && len(pod.Spec.Containers) >= 1 {
			continue
		}
		for _, port := range pod.Spec.Containers[0].Ports {
			if port.Name != portName {
				continue
			}
			mongoPort := strconv.Itoa(int(port.HostPort))
			hosts = append(hosts, pod.Status.HostIP+":"+mongoPort)
			break
		}
	}
	if len(hosts) > 0 {
		return "mongodb://" + strings.Join(hosts, ",")
	}
	return ""
}
