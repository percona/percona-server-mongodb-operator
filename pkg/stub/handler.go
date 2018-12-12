package stub

import (
	"context"
	"time"

	sdk "github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	podk8s "github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	watchdog "github.com/percona/mongodb-orchestration-tools/watchdog"
	wdConfig "github.com/percona/mongodb-orchestration-tools/watchdog/config"
	wdMetrics "github.com/percona/mongodb-orchestration-tools/watchdog/metrics"

	opSdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

var ReplsetInitWait = 10 * time.Second

const minPersistentVolumeClaims = 1

// NewHandler return new instance of sdk.Handler interface.
func NewHandler(client sdk.Client) opSdk.Handler {
	return &Handler{
		client:       client,
		startedAt:    time.Now(),
		watchdogQuit: make(chan bool, 1),
	}
}

// Handler implements sdk.Handler interface.
type Handler struct {
	client        sdk.Client
	serverVersion *v1alpha1.ServerVersion
	pods          *podk8s.Pods
	watchdog      *watchdog.Watchdog
	watchdogQuit  chan bool
	startedAt     time.Time
}

// ensureWatchdog ensures the PSMDB watchdog has started. This process controls the replica set and sharding
// state of a PSMDB cluster.
//
// See: https://github.com/percona/mongodb-orchestration-tools/tree/master/watchdog
//
func (h *Handler) ensureWatchdog(psmdb *v1alpha1.PerconaServerMongoDB, usersSecret *corev1.Secret) error {
	// Skip if watchdog is started
	if h.watchdog != nil {
		return nil
	}

	if h.pods == nil {
		h.pods = podk8s.NewPods(psmdb.Name, psmdb.Namespace)
	}

	// Start the watchdog if it has not been started
	metricsCollector := wdMetrics.NewCollector()
	h.watchdog = watchdog.New(&wdConfig.Config{
		ServiceName:    psmdb.Name,
		Username:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminUser]),
		Password:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminPassword]),
		APIPoll:        5 * time.Second,
		ReplsetPoll:    5 * time.Second,
		ReplsetTimeout: 3 * time.Second,
	}, h.pods, metricsCollector, &h.watchdogQuit)
	go h.watchdog.Run()

	// register prometheus collector
	prometheus.MustRegister(metricsCollector)

	return nil
}

// Handle is the main operator function that is ran for every SDK event
func (h *Handler) Handle(ctx context.Context, event opSdk.Event) error {
	switch o := event.Object.(type) {
	case *v1alpha1.PerconaServerMongoDB:
		psmdb := o

		// apply Spec defaults
		h.addPSMDBSpecDefaults(psmdb)

		// Ignore the delete event since the garbage collector will clean up all secondary resources for the CR
		// All secondary resources must have the CR set as their OwnerReference for this to be the case
		if event.Deleted {
			logrus.Infof("received deleted event for %s", psmdb.Name)
			if h.watchdog != nil {
				close(h.watchdogQuit)
				h.watchdog = nil
			}
			return nil
		}

		// Get server/platform info if not exists
		if h.serverVersion == nil {
			serverVersion, err := getServerVersion()
			if err != nil {
				logrus.Errorf("error fetching server/platform version info: %v", err)
				return err
			}
			h.serverVersion = serverVersion
			logrus.Infof("detected Kubernetes platform: %s, version: %s", h.getPlatform(psmdb), h.serverVersion.Info)
		}

		// Create the mongodb internal auth key if it doesn't exist
		err := h.client.Create(newPSMDBMongoKeySecret(o))
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				logrus.Errorf("failed to create psmdb auth key: %v", err)
				return err
			}
		} else {
			logrus.Info("created mongodb auth key secret")
		}

		// Load MongoDB system users/passwords from secret
		usersSecret, err := getPSMDBSecret(psmdb, h.client, psmdb.Spec.Secrets.Users)
		if err != nil {
			logrus.Errorf("failed to load psmdb user secrets: %v", err)
			return err
		}

		err = h.ensureWatchdog(psmdb, usersSecret)
		if err != nil {
			return err
		}

		// Ensure all replica sets exist. When sharding is supported this
		// loop will create the cluster shards and config server replset
		clusterPods := make([]corev1.Pod, 0)
		clusterSets := make([]appsv1.StatefulSet, 0)
		for _, replset := range psmdb.Spec.Replsets {
			// Update the PSMDB status
			podsList, err := h.updateStatus(psmdb, replset, usersSecret)
			if err != nil {
				logrus.Errorf("failed to update psmdb status for replset %s: %v", replset.Name, err)
				return err
			}
			clusterPods = append(clusterPods, podsList.Items...)

			// Ensure replset exists and has correct state, PVCs, etc
			set, err := h.ensureReplset(psmdb, podsList, replset, usersSecret)
			if err != nil {
				if err == ErrNoRunningMongodContainers {
					logrus.Debugf("no running mongod containers for replset %s, skipping replset initiation", replset.Name)
					continue
				}
				logrus.Errorf("failed to ensure replset %s: %v", replset.Name, err)
				return err
			}
			clusterSets = append(clusterSets, *set)
		}

		// Update the pods+statefulsets list that is read by the watchdog
		h.pods.Update(clusterPods, clusterSets)
	}
	return nil
}
