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
	"k8s.io/apimachinery/pkg/types"
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
			HolderIdentity: new("sync-uid"),
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

func TestTryAcquireLease(t *testing.T) {
	const (
		ns          = "ns"
		clusterName = "tgt"
	)

	cluster := &psmdbv1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: ns},
	}
	newBackup := &psmdbv1.PerconaServerMongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backup-new",
			Namespace: ns,
			UID:       types.UID("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"),
		},
		Spec: psmdbv1.PerconaServerMongoDBBackupSpec{ClusterName: clusterName},
	}

	tests := map[string]struct {
		holderBackup *psmdbv1.PerconaServerMongoDBBackup
		seedLease    bool
		wantAcquired bool
		wantHolder   string
	}{
		"no lease: acquires": {
			wantAcquired: true,
			wantHolder:   naming.BackupHolderId(newBackup),
		},
		"lease held by running backup: waits": {
			holderBackup: backupWithState("backup-old", ns, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", clusterName, psmdbv1.BackupStateRunning),
			seedLease:    true,
			wantAcquired: false,
		},
		"lease held by error backup: takes stale lease": {
			holderBackup: backupWithState("backup-old", ns, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", clusterName, psmdbv1.BackupStateError),
			seedLease:    true,
			wantAcquired: true,
			wantHolder:   naming.BackupHolderId(newBackup),
		},
		"lease held by missing backup: takes stale lease": {
			seedLease:    true,
			wantAcquired: true,
			wantHolder:   naming.BackupHolderId(newBackup),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			objs := []runtime.Object{newBackup.DeepCopy()}
			holder := "missing-backup-aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
			if tc.holderBackup != nil {
				objs = append(objs, tc.holderBackup.DeepCopy())
				holder = naming.BackupHolderId(tc.holderBackup)
			}
			if tc.seedLease {
				objs = append(objs, &coordv1.Lease{
					ObjectMeta: metav1.ObjectMeta{
						Name:      naming.BackupLeaseName(clusterName),
						Namespace: ns,
					},
					Spec: coordv1.LeaseSpec{
						AcquireTime:    &metav1.MicroTime{Time: time.Now()},
						HolderIdentity: new(holder),
					},
				})
			}

			cl := fake.NewClientBuilder().WithScheme(backupScheme(t)).WithRuntimeObjects(objs...).Build()
			r := &ReconcilePerconaServerMongoDBBackup{client: cl}

			acquired, err := r.tryAcquireLease(t.Context(), newBackup.DeepCopy(), cluster)
			require.NoError(t, err)
			assert.Equal(t, tc.wantAcquired, acquired)

			got := &coordv1.Lease{}
			require.NoError(t, cl.Get(context.Background(), types.NamespacedName{
				Name:      naming.BackupLeaseName(clusterName),
				Namespace: ns,
			}, got))
			require.NotNil(t, got.Spec.HolderIdentity)
			if tc.wantHolder != "" {
				assert.Equal(t, tc.wantHolder, *got.Spec.HolderIdentity)
				return
			}
			assert.Equal(t, holder, *got.Spec.HolderIdentity)
		})
	}
}

func backupWithState(name, namespace, uid, clusterName string, state psmdbv1.BackupState) *psmdbv1.PerconaServerMongoDBBackup {
	return &psmdbv1.PerconaServerMongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID(uid),
		},
		Spec:   psmdbv1.PerconaServerMongoDBBackupSpec{ClusterName: clusterName},
		Status: psmdbv1.PerconaServerMongoDBBackupStatus{State: state},
	}
}

func stringPtr(s string) *string { return &s }
