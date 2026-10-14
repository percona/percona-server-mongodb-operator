package perconaservermongodbrestore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func restoreScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, coordv1.AddToScheme(s))
	require.NoError(t, psmdbv1.SchemeBuilder.AddToScheme(s))
	return s
}

func TestCheckClusterSyncLease(t *testing.T) {
	const (
		ns          = "ns"
		clusterName = "tgt"
	)
	holder := "sync-uid"
	csLease := &coordv1.Lease{
		Name:      naming.ClusterSyncLeaseName(clusterName),
		Namespace: ns,
		Spec: coordv1.LeaseSpec{
			AcquireTime:    &metav1.MicroTime{Time: time.Now()},
			HolderIdentity: &holder,
		},
	}
	cr := &psmdbv1.PerconaServerMongoDBRestore{
		Name: "rst", Namespace: ns,
	}
	cluster := &psmdbv1.PerconaServerMongoDB{
		Name: clusterName, Namespace: ns,
	}

	tests := map[string]struct {
		seedLease                bool
		clusterSyncAlreadyActive bool
		wantBlocked              bool
		wantEvent                bool
	}{
		"clustersync lease active: blocked, event emitted": {
			seedLease:   true,
			wantBlocked: true,
			wantEvent:   true,
		},
		"clustersync lease active: blocked, no repeated event": {
			seedLease:                true,
			clusterSyncAlreadyActive: true,
			wantBlocked:              true,
		},
		"no clustersync lease: not blocked, no event": {
			seedLease:   false,
			wantBlocked: false,
			wantEvent:   false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			restore := cr.DeepCopy()
			if tc.clusterSyncAlreadyActive {
				meta.SetStatusCondition(&restore.Status.Conditions, metav1.Condition{
					Type:   psmdbv1.ConditionClusterSyncActive,
					Status: metav1.ConditionTrue,
				})
			}
			objs := []runtime.Object{}
			if tc.seedLease {
				objs = append(objs, csLease)
			}
			cl := fake.NewClientBuilder().WithScheme(restoreScheme(t)).WithRuntimeObjects(objs...).Build()
			recorder := record.NewFakeRecorder(4)
			r := &ReconcilePerconaServerMongoDBRestore{client: cl, recorder: recorder}

			blocked, err := r.checkClusterSyncLease(context.Background(), restore, cluster)
			require.NoError(t, err)
			assert.Equal(t, tc.wantBlocked, blocked)

			if tc.wantEvent {
				select {
				case ev := <-recorder.Events:
					assert.Contains(t, ev, "ClusterSyncActive")
					assert.Contains(t, ev, clusterName)
				default:
					t.Fatal("expected ClusterSyncActive event, none emitted")
				}
				return
			}
			select {
			case ev := <-recorder.Events:
				t.Fatalf("unexpected event emitted: %s", ev)
			default:
			}
		})
	}
}

func TestCheckRestoreLocksBackupLease(t *testing.T) {
	const (
		ns          = "ns"
		clusterName = "tgt"
	)
	holder := "backup-uid"
	lease := &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.BackupLeaseName(clusterName),
			Namespace: ns,
		},
		Spec: coordv1.LeaseSpec{HolderIdentity: &holder},
	}
	cl := fake.NewClientBuilder().WithScheme(restoreScheme(t)).WithRuntimeObjects(lease).Build()
	r := &ReconcilePerconaServerMongoDBRestore{client: cl}
	cluster := &psmdbv1.PerconaServerMongoDB{Name: clusterName, Namespace: ns}

	waitingReason, err := r.checkRestoreLocks(context.Background(), cluster)
	require.NoError(t, err)
	assert.Equal(t, psmdbv1.RestoreWaitingReasonBackupLease, waitingReason)
}
