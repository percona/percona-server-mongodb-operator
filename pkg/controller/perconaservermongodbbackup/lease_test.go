package perconaservermongodbbackup

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func backupScheme(t *testing.T) *runtime.Scheme {
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
	activeLease := &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.ClusterSyncLeaseName(clusterName),
			Namespace: ns,
		},
		Spec: coordv1.LeaseSpec{
			AcquireTime:    &metav1.MicroTime{Time: time.Now()},
			HolderIdentity: stringPtr("sync-uid"),
		},
	}

	tests := map[string]struct {
		state       psmdbv1.BackupState
		seedLease   bool
		wantBlocked bool
		wantEvent   bool
	}{
		"New + clustersync lease active: blocked, event emitted": {
			state:       psmdbv1.BackupStateNew,
			seedLease:   true,
			wantBlocked: true,
			wantEvent:   true,
		},
		"Waiting + clustersync lease active: blocked, event emitted": {
			state:       psmdbv1.BackupStateWaiting,
			seedLease:   true,
			wantBlocked: true,
			wantEvent:   true,
		},
		"Running + clustersync lease active: NOT blocked (in-flight backups keep going)": {
			state:       psmdbv1.BackupStateRunning,
			seedLease:   true,
			wantBlocked: false,
			wantEvent:   false,
		},
		"Ready + clustersync lease active: NOT blocked": {
			state:       psmdbv1.BackupStateReady,
			seedLease:   true,
			wantBlocked: false,
			wantEvent:   false,
		},
		"New + no clustersync lease: NOT blocked": {
			state:       psmdbv1.BackupStateNew,
			seedLease:   false,
			wantBlocked: false,
			wantEvent:   false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &psmdbv1.PerconaServerMongoDBBackup{
				ObjectMeta: metav1.ObjectMeta{Name: "bcp", Namespace: ns},
				Status:     psmdbv1.PerconaServerMongoDBBackupStatus{State: tc.state},
			}
			cluster := &psmdbv1.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: ns},
			}

			objs := []runtime.Object{}
			if tc.seedLease {
				objs = append(objs, activeLease)
			}
			cl := fake.NewClientBuilder().WithScheme(backupScheme(t)).WithRuntimeObjects(objs...).Build()
			recorder := record.NewFakeRecorder(4)
			r := &ReconcilePerconaServerMongoDBBackup{client: cl, recorder: recorder}

			blocked, err := r.checkClusterSyncLease(context.Background(), cr, cluster)
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

func stringPtr(s string) *string { return &s }
