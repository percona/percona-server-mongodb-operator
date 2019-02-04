package stub

import (
	"context"
	"fmt"
	"time"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/stub/backup"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	podk8s "github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	"github.com/percona/mongodb-orchestration-tools/watchdog"
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

// Handler implements sdk.Handler interface.
type Handler struct {
	client          sdk.Client
	serverVersion   *v1alpha1.ServerVersion
	pods            *podk8s.Pods
	watchdog        map[string]*watchdog.Watchdog
	watchdogMetrics *wdMetrics.Collector
	watchdogQuit    chan bool
	startedAt       time.Time
	backups         *backup.Controller
}

// NewHandler return new instance of sdk.Handler interface.
func NewHandler(client sdk.Client) opSdk.Handler {
	return &Handler{
		client:          client,
		startedAt:       time.Now(),
		watchdogMetrics: wdMetrics.NewCollector(),
		watchdogQuit:    make(chan bool, 1),
		watchdog:        make(map[string]*watchdog.Watchdog),
	}
}

// ensureWatchdog ensures the PSMDB watchdog has started. This process controls the replica set and sharding
// state of a PSMDB cluster.
//
// See: https://github.com/percona/mongodb-orchestration-tools/tree/master/watchdog
//
func (h *Handler) ensureWatchdog(psmdb *v1alpha1.PerconaServerMongoDB, usersSecret *corev1.Secret) error {
	// Skip if watchdog is started
	if _, ok := h.watchdog[psmdb.Name]; ok {
		return nil
	}

	// Skip if there are no initialized replsets
	var doStart bool
	for _, replset := range psmdb.Status.Replsets {
		if replset.Initialized {
			doStart = true
			break
		}
	}
	if !doStart {
		return nil
	}

	// Start the watchdog if it has not been started
	h.watchdog[psmdb.Name] = watchdog.New(&wdConfig.Config{
		Username:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminUser]),
		Password:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminPassword]),
		APIPoll:        5 * time.Second,
		ReplsetPoll:    5 * time.Second,
		ReplsetTimeout: 3 * time.Second,
	}, h.pods, h.watchdogMetrics, h.watchdogQuit)
	go h.watchdog[psmdb.Name].Run()

	if len(h.watchdog) == 1 {
		// register prometheus collector
		prometheus.MustRegister(h.watchdogMetrics)
		logrus.Debug("Registered watchdog Prometheus collector")
	}
	return nil
}

// Handle is the main operator function that is ran for every SDK event
func (h *Handler) Handle(ctx context.Context, event opSdk.Event) error {
	switch o := event.Object.(type) {
	case *v1alpha1.PerconaServerMongoDB:
		psmdb := o

		// apply Spec defaults
		h.addSpecDefaults(psmdb)

		// Setup watchdog 'k8s' pod source and CustomResourceState struct for CR
		// (https://github.com/percona/mongodb-orchestration-tools/blob/master/pkg/pod/pod.go#L51-L56)
		if h.pods == nil {
			h.pods = podk8s.NewPods(psmdb.Namespace)
		}
		crState := &podk8s.CustomResourceState{
			Name:         psmdb.Name,
			Pods:         make([]corev1.Pod, 0),
			Services:     make([]corev1.Service, 0),
			Statefulsets: make([]appsv1.StatefulSet, 0),
		}

		// Delete CR if the user has removed it. Otherwise, ignore the delete event since the garbage
		// collector will clean up all secondary resources for the CR. All secondary resources must
		// have the CR set as their OwnerReference for this to be the case
		if event.Deleted {
			logrus.Infof("received deleted event for %s", psmdb.Name)

			for _, rsSpec := range psmdb.Spec.Replsets {
				logrus.Debugf("stopping watchdog watchers for psmdb CR %s, replset %s", psmdb.Name, rsSpec.Name)
				h.watchdog[psmdb.Name].StopWatcher(psmdb.Name, rsSpec.Name)
			}

			h.pods.Delete(crState)

			delete(h.watchdog, psmdb.Name)

			return nil
		}

		// Get server/platform info if not exists
		if h.serverVersion == nil {
			serverVersion, err := util.GetServerVersion()
			if err != nil {
				logrus.Errorf("error fetching server/platform version info: %v", err)
				return err
			}
			h.serverVersion = serverVersion
			logrus.Infof("detected Kubernetes platform: %s, version: %s", util.GetPlatform(psmdb, h.serverVersion), h.serverVersion.Info)
		}

		// Create the mongodb internal auth key if it doesn't exist
		err := h.client.Create(newMongoKeySecret(o))
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				logrus.Errorf("failed to create psmdb auth key: %v", err)
				return err
			}
		} else {
			logrus.Info("created mongodb auth key secret")
		}

		// Load MongoDB system users/passwords from secret
		usersSecret, err := util.GetSecret(psmdb, h.client, psmdb.Spec.Secrets.Users)
		if err != nil {
			logrus.Errorf("failed to load psmdb user secrets: %v", err)
			return err
		}

		// Ensure the backup coordinator is started
		if psmdb.Spec.Backup != nil && psmdb.Spec.Backup.Enabled {
			h.backups = backup.New(h.client, psmdb, h.serverVersion, usersSecret)
			err = h.backups.EnsureCoordinator()
			if err != nil {
				logrus.Errorf("failed to start/update backup coordinator: %v", err)
				return err
			}
		}

		// Ensure scheduled backup tasks are created/updated
		if h.backups != nil && h.hasReplsetsInitialized(psmdb) {
			err = h.backups.EnsureBackupTasks()
			if err != nil {
				return err
			}
		}

		// Ensure the watchdog is started (to contol the MongoDB Replica Set config)
		err = h.ensureWatchdog(psmdb, usersSecret)
		if err != nil {
			return err
		}

		// Ensure all replica sets exist. When sharding is supported this
		// loop will create the cluster shards and config server replset
		var hasRunningBackupAgents bool
		for i, replset := range psmdb.Spec.Replsets {
			// multiple replica sets is not supported until sharding is
			// added to the operator
			if i > 0 {
				logrus.Errorf("multiple replica sets is not yet supported, skipping replset: %s", replset.Name)
				continue
			}

			// Update the PSMDB status
			podsList, err := h.updateStatus(psmdb, replset, usersSecret)
			if err != nil {
				logrus.Errorf("failed to update psmdb status for replset %s: %v", replset.Name, err)
				return err
			}
			crState.Pods = append(crState.Pods, podsList.Items...)

			// Ensure replset has external services
			if replset.Expose != nil && replset.Expose.Enabled {

				logrus.Infof("Creating services for replset %s", replset.Name)
				if err := h.createSvcs(psmdb, replset); err != nil {
					return fmt.Errorf("failed to create services of replset %s: %v", replset.Name, err)
				}

				logrus.Infof("Receiving services list for replset %s", replset.Name)
				svcs, err := h.svcList(psmdb, replset, false)
				if err != nil {
					return fmt.Errorf("failed to fetch services of replset %s: %v", replset.Name, err)
				}

				logrus.Infof("Trying to bind pods to services for replset %s", replset.Name)
				if err := h.bindSvcs(svcs, podsList); err != nil {
					return fmt.Errorf("failed to bind pods to services of replset %s: %v", replset.Name, err)
				}
			}

			// Ensure replset exists and has correct state, PVCs, etc
			sets, err := h.ensureReplset(psmdb, podsList, replset, usersSecret)
			if err != nil {
				if err == ErrNoRunningMongodContainers {
					logrus.Debugf("no running mongod containers for replset %s, skipping replset initiation", replset.Name)
					continue
				}
				logrus.Errorf("failed to ensure replset %s: %v", replset.Name, err)
				continue
			}

			set, ok := sets["mongod"]
			if !ok {
				return fmt.Errorf("no mongod statefullsets has found")
			}
			crState.Statefulsets = append(crState.Statefulsets, *set)

			if replset.Arbiter != nil && replset.Arbiter.Enabled && replset.Arbiter.Size >= 1 {
				arbiter, ok := sets["arbiter"]
				if !ok {
					return fmt.Errorf("no arbiter statefullsets has found")
				}
				crState.Statefulsets = append(crState.Statefulsets, *arbiter)
			}

			svc, err := h.svcList(psmdb, replset, true)
			if err != nil {
				logrus.Errorf("failed to fetch services for replset %s: %v", replset.Name, err)
				return err
			}

			crState.Services = append(crState.Services, svc.Items...)

			// Check if any pod has a backup agent container running (has not terminated)
			for _, pod := range podsList.Items {
				if util.GetPodContainerStatus(&pod.Status, backup.AgentContainerName) != nil {
					terminated, err := util.IsContainerTerminated(&pod.Status, backup.AgentContainerName)
					if err != nil {
						logrus.Errorf("failed to find backup agent container for replset %s, pod %s: %v",
							replset.Name, pod.Name, err,
						)
					}
					if !terminated {
						hasRunningBackupAgents = true
						break
					}
				}
			}
		}

		// If backups are disabled, remove backup task/cronJobs and the backup coordinator
		// if there are no running backup agents in replica sets
		if (psmdb.Spec.Backup == nil || !psmdb.Spec.Backup.Enabled) && h.backups != nil {
			err = h.backups.DeleteBackupTasks()
			if err != nil {
				logrus.Errorf("failed to remove backup cronJobs: %v", err)
			}

			if !hasRunningBackupAgents {
				err = h.backups.DeleteCoordinator()
				if err != nil {
					logrus.Errorf("failed to remove backup coordinator: %v", err)
					return err
				}
				h.backups = nil
			}
		}

		// Update the PSMDB CR state that is read by the watchdog
		h.pods.Update(crState)
	}
	return nil
}
