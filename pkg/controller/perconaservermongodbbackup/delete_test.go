package perconaservermongodbbackup

import (
	"context"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

func TestReconcileDeleteClearsFinalizersWhenPBMUnavailable(t *testing.T) {
	const (
		ns          = "ns"
		clusterName = "restored-mongodb"
		backupName  = "backup-error"
	)

	now := metav1.NewTime(time.Now())
	cr := &psmdbv1.PerconaServerMongoDBBackup{
		Name:              backupName,
		Namespace:         ns,
		UID:               types.UID("cccccccc-cccc-cccc-cccc-cccccccccccc"),
		DeletionTimestamp: &now,
		Finalizers: []string{
			naming.FinalizerDeleteBackup,
			naming.FinalizerReleaseLock,
		},
		Spec: psmdbv1.PerconaServerMongoDBBackupSpec{
			ClusterName: clusterName,
			StorageName: "s3-storage",
		},
		Status: psmdbv1.PerconaServerMongoDBBackupStatus{
			State: psmdbv1.BackupStateError,
			Error: "create pbm object: create PBM connection: mongo: no documents in result",
		},
	}

	cluster := &psmdbv1.PerconaServerMongoDB{
		Name:      clusterName,
		Namespace: ns,
		Spec: psmdbv1.PerconaServerMongoDBSpec{
			CRVersion: "1.21.0",
		},
	}

	lease := &coordv1.Lease{
		Name:      naming.BackupLeaseName(clusterName),
		Namespace: ns,
		Spec: coordv1.LeaseSpec{
			AcquireTime:    &metav1.MicroTime{Time: time.Now()},
			HolderIdentity: new(naming.BackupHolderId(cr)),
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(backupScheme(t)).
		WithRuntimeObjects(cr.DeepCopy(), cluster.DeepCopy(), lease.DeepCopy()).
		WithStatusSubresource(cr).
		Build()

	r := &ReconcilePerconaServerMongoDBBackup{
		client:    cl,
		apiReader: cl,
		newPBMFunc: func(ctx context.Context, c client.Client, cluster *psmdbv1.PerconaServerMongoDB) (backup.PBM, error) {
			return nil, errors.New("create PBM connection: mongo: no documents in result")
		},
	}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		Name: backupName, Namespace: ns,
	})
	require.NoError(t, err)

	got := &psmdbv1.PerconaServerMongoDBBackup{}
	err = cl.Get(context.Background(), types.NamespacedName{Name: backupName, Namespace: ns}, got)
	// Fake client removes the object once finalizers are cleared and DeletionTimestamp is set.
	// Either outcome proves finalizers no longer block deletion.
	if err == nil {
		assert.Empty(t, got.GetFinalizers(), "finalizers must be cleared even when PBM setup fails")
		return
	}
	require.NoError(t, client.IgnoreNotFound(err))
}
