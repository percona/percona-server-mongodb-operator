package stub

import (
	"context"
	"time"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	pkgSdk "github.com/Percona-Lab/percona-server-mongodb-operator/pkg/sdk"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	podk8s "github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	watchdog "github.com/percona/mongodb-orchestration-tools/watchdog"
	wdConfig "github.com/percona/mongodb-orchestration-tools/watchdog/config"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

var ReplsetInitWait = 10 * time.Second

const minPersistentVolumeClaims = 1

func NewHandler(client pkgSdk.Client) sdk.Handler {
	return &Handler{
		client:       client,
		startedAt:    time.Now(),
		watchdogQuit: make(chan bool, 1),
	}
}

type Handler struct {
	client        pkgSdk.Client
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
func (h *Handler) ensureWatchdog(m *v1alpha1.PerconaServerMongoDB, usersSecret *corev1.Secret) error {
	// Skip if watchdog is started
	if h.watchdog != nil {
		return nil
	}

	// Start the watchdog if it has not been started
	h.watchdog = watchdog.New(&wdConfig.Config{
		ServiceName:    m.Name,
		Username:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminUser]),
		Password:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminPassword]),
		APIPoll:        5 * time.Second,
		ReplsetPoll:    5 * time.Second,
		ReplsetTimeout: 3 * time.Second,
	}, &h.watchdogQuit, h.pods)
	go h.watchdog.Run()

	return nil
}

// Handle is the main operator function that is ran for every SDK event
func (h *Handler) Handle(ctx context.Context, event sdk.Event) error {
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
			logrus.Infof("detected Kubernetes platform: %s, version: %s", getPlatform(psmdb, h.serverVersion), h.serverVersion.Info)
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

		// Ensure all replica sets exist. When sharding is supported this
		// loop will create the cluster shards and config server replset
		for _, replset := range psmdb.Spec.Replsets {
			// Update the PSMDB status
			podList, err := h.updateStatus(psmdb, replset, usersSecret)
			if err != nil {
				logrus.Errorf("failed to update psmdb status for replset %s: %v", replset.Name, err)
				return err
			}

			// Ensure replset exists and has correct state, PVCs, etc
			err = h.ensureReplset(psmdb, podList, replset, usersSecret)
			if err != nil {
				if err == ErrNoRunningMongodContainers {
					logrus.Debugf("no running mongod containers for replset %s, skipping replset initiation", replset.Name)
					continue
				}
				logrus.Errorf("failed to ensure replset %s: %v", replset.Name, err)
				return err
			}
		}
	}
	return nil
}
