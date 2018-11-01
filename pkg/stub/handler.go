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
	"k8s.io/apimachinery/pkg/api/errors"
)

var ReplsetInitWait = 10 * time.Second

func NewHandler(client pkgSdk.Client) sdk.Handler {
	return &Handler{
		client:       client,
		startedAt:    time.Now(),
		watchdogQuit: make(chan bool, 1),
	}
}

type Handler struct {
	client       pkgSdk.Client
	pods         *podk8s.Pods
	watchdog     *watchdog.Watchdog
	watchdogQuit chan bool
	startedAt    time.Time
}

func (h *Handler) ensureWatchdog(m *v1alpha1.PerconaServerMongoDB) error {
	if h.watchdog != nil {
		return nil
	}

	// load username/password from secret
	secret, err := getPSMDBSecret(m, h.client, m.Spec.Secrets.Users)
	if err != nil {
		logrus.Errorf("failed to load psmdb user secrets: %v", err)
		return err
	}

	// Start the watchdog if it has not been started
	h.watchdog = watchdog.New(&wdConfig.Config{
		ServiceName:    m.Name,
		Username:       string(secret.Data[motPkg.EnvMongoDBClusterAdminUser]),
		Password:       string(secret.Data[motPkg.EnvMongoDBClusterAdminPassword]),
		APIPoll:        5 * time.Second,
		ReplsetPoll:    5 * time.Second,
		ReplsetTimeout: 3 * time.Second,
	}, &h.watchdogQuit, h.pods)
	go h.watchdog.Run()

	return nil
}

func (h *Handler) Handle(ctx context.Context, event sdk.Event) error {
	switch o := event.Object.(type) {
	case *v1alpha1.PerconaServerMongoDB:
		psmdb := o

		// Ignore the delete event since the garbage collector will clean up all secondary resources for the CR
		// All secondary resources must have the CR set as their OwnerReference for this to be the case
		if event.Deleted {
			logrus.Infof("Received deleted event for %s", psmdb.Name)
			h.watchdogQuit <- true
			h.watchdog = nil
			return nil
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

		// Ensure all replica sets exist. When sharding is supported this
		// loop will create the cluster shards and config server replset
		replsetNames := []string{psmdb.Spec.Mongod.ReplsetName}
		for _, replsetName := range replsetNames {
			err = h.ensureReplset(psmdb, replsetName)
			if err != nil {
				logrus.Errorf("failed to ensure replset %s: %v", replsetName, err)
				return err
			}
		}
	}
	return nil
}
