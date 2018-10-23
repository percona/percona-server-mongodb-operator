package stub

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	podk8s "github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	watchdog "github.com/percona/mongodb-orchestration-tools/watchdog"
	wdConfig "github.com/percona/mongodb-orchestration-tools/watchdog/config"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

var ReplsetInitWait = 10 * time.Second

func NewHandler() sdk.Handler {
	return &Handler{
		startedAt:    time.Now(),
		watchdogQuit: make(chan bool, 1),
	}
}

type Handler struct {
	pods         *podk8s.Pods
	watchdog     *watchdog.Watchdog
	watchdogQuit chan bool
	startedAt    time.Time
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

		// Create the StatefulSet if it doesn't exist
		set, err := newPSMDBStatefulSet(o)
		if err != nil {
			logrus.Errorf("failed to create stateful set object for replset %s: %v", psmdb.Spec.Mongod.ReplsetName, err)
			return err
		}
		err = sdk.Create(set)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				logrus.Errorf("failed to create stateful set for replset %s: %v", psmdb.Spec.Mongod.ReplsetName, err)
				return err
			}
		} else {
			logrus.WithFields(logrus.Fields{
				"limit_cpu":     psmdb.Spec.Mongod.Limits.Cpu,
				"limit_memory":  psmdb.Spec.Mongod.Limits.Memory,
				"limit_storage": psmdb.Spec.Mongod.Limits.Storage,
			}).Infof("created stateful set for replset: %s", psmdb.Spec.Mongod.ReplsetName)
		}

		// Ensure the stateful set size is the same as the spec
		err = sdk.Get(set)
		if err != nil {
			return fmt.Errorf("failed to get stateful set for replset %s: %v", psmdb.Spec.Mongod.ReplsetName, err)
		}
		size := psmdb.Spec.Mongod.Size
		if *set.Spec.Replicas != size {
			logrus.Infof("setting replicas to %d for replset: %s", size, psmdb.Spec.Mongod.ReplsetName)
			set.Spec.Replicas = &size
			err = sdk.Update(set)
			if err != nil {
				return fmt.Errorf("failed to update stateful set for replset %s: %v", psmdb.Spec.Mongod.ReplsetName, err)
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
			logrus.Infof("created mongodb service for replset: %s", psmdb.Spec.Mongod.ReplsetName)
		}

		// Update the PerconaServerMongoDB status with the pod names and pod mongodb uri
		podList := podList()
		labelSelector := labels.SelectorFromSet(labelsForPerconaServerMongoDB(psmdb)).String()
		listOps := &metav1.ListOptions{LabelSelector: labelSelector}
		err = sdk.List(psmdb.Namespace, podList, sdk.WithListOptions(listOps))
		if err != nil {
			return fmt.Errorf("failed to list pods for replset %s: %v", psmdb.Spec.Mongod.ReplsetName, err)
		}
		podNames := getPodNames(podList.Items)
		if len(psmdb.Status.Replsets) == 0 {
			psmdb.Status.Replsets = []*v1alpha1.ReplsetStatus{
				{
					Name: psmdb.Spec.Mongod.ReplsetName,
				},
			}
		}
		if !reflect.DeepEqual(podNames, psmdb.Status.Replsets[0].Members) {
			psmdb.Status.Replsets[0].Members = podNames
			psmdb.Status.Replsets[0].Uri = getMongoURI(podList.Items, mongodPortName)
			err := sdk.Update(psmdb)
			if err != nil {
				return fmt.Errorf("failed to update status for replset %s: %v", psmdb.Spec.Mongod.ReplsetName, err)
			}
		}

		// Update the pods list that is read by the watchdog
		if h.pods == nil {
			h.pods = podk8s.NewPods(psmdb.Name, psmdb.Namespace, mongodPortName)
		}
		h.pods.SetPods(podList.Items)

		// Initiate the replset if it hasn't already been initiated + there are pods +
		// we have waited the ReplsetInitWait period since starting
		if !psmdb.Status.Replsets[0].Initialised && len(podList.Items) >= 1 && time.Since(h.startedAt) > ReplsetInitWait {
			err = h.handleReplsetInit(psmdb, podList.Items)
			if err != nil {
				logrus.Errorf("failed to init replset %s: %v", psmdb.Spec.Mongod.ReplsetName, err)
				return nil
			}

			// update status after replset init
			psmdb.Status.Replsets[0].Initialised = true
			err = sdk.Update(psmdb)
			if err != nil {
				return fmt.Errorf("failed to update status for replset %s: %v", psmdb.Spec.Mongod.ReplsetName, err)
			}
			logrus.Infof("changed state to initialised for replset %s", psmdb.Spec.Mongod.ReplsetName)

			if h.watchdog == nil {
				// load username/password from secret
				secret, err := getPSMDBSecret(psmdb, psmdb.Spec.Secrets.Users)
				if err != nil {
					logrus.Errorf("failed to load psmdb user secrets: %v", err)
					return err
				}

				// Start the watchdog if it has not been started
				h.watchdog = watchdog.New(&wdConfig.Config{
					ServiceName:    psmdb.Name,
					Username:       string(secret.Data[motPkg.EnvMongoDBClusterAdminUser]),
					Password:       string(secret.Data[motPkg.EnvMongoDBClusterAdminPassword]),
					APIPoll:        5 * time.Second,
					ReplsetPoll:    5 * time.Second,
					ReplsetTimeout: 3 * time.Second,
				}, &h.watchdogQuit, h.pods)
				go h.watchdog.Run()
			}
		}
	}
	return nil
}
