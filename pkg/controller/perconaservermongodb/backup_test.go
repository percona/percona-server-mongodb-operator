package perconaservermongodb

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestOldScheduledBackups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		states      []api.BackupState
		keep        int
		wantDeleted []string
	}{
		{
			name:        "prune a newer canceled backup before ready backups",
			states:      []api.BackupState{api.BackupStateReady, api.BackupStateReady, api.BackupStateCanceled},
			keep:        1,
			wantDeleted: []string{"backup-3", "backup-1"},
		},
		{
			name:        "keep the newest backup when all are canceled",
			states:      []api.BackupState{api.BackupStateCanceled, api.BackupStateCanceled, api.BackupStateCanceled},
			keep:        1,
			wantDeleted: []string{"backup-1", "backup-2"},
		},
		{
			name:   "keep canceled backups within the limit",
			states: []api.BackupState{api.BackupStateCanceled, api.BackupStateRunning},
			keep:   1,
		},
		{
			name: "ignore active and failed backups",
			states: []api.BackupState{
				api.BackupStateNew,
				api.BackupStateWaiting,
				api.BackupStateRequested,
				api.BackupStateRunning,
				api.BackupStateCanceling,
				api.BackupStateError,
				api.BackupStateCanceled,
				api.BackupStateReady,
			},
			keep:        1,
			wantDeleted: []string{"backup-7"},
		},
	}

	cr := &api.PerconaServerMongoDB{
		Name: "cluster", Namespace: "ns",
		Spec: api.PerconaServerMongoDBSpec{CRVersion: version.Version()},
	}
	task := &api.BackupTaskSpec{Name: "daily", StorageName: "s3"}
	scheduled, err := backup.BackupFromTask(cr, task)
	require.NoError(t, err)
	scheduled.Namespace = cr.Namespace

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []client.Object
			for i, state := range tt.states {
				position := i + 1
				bcp := scheduled.DeepCopy()
				bcp.Name = fmt.Sprintf("backup-%d", position)
				bcp.CreationTimestamp = metav1.Unix(int64(position), 0)
				bcp.Status.State = state
				objects = append(objects, bcp)
			}
			r := buildFakeClient(objects...)

			toDelete, err := r.oldScheduledBackups(t.Context(), cr, task.Name, tt.keep)
			require.NoError(t, err)

			var names []string
			for _, backup := range toDelete {
				names = append(names, backup.Name)
			}
			assert.Equal(t, tt.wantDeleted, names)
		})
	}
}

func TestIsBackupRunning(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const (
		crName = "some-name"
		ns     = "psmdb"
	)

	cr := &api.PerconaServerMongoDB{
		Name: crName, Namespace: ns,
	}

	backup := func(name, clusterName string, state api.BackupState) *api.PerconaServerMongoDBBackup {
		return &api.PerconaServerMongoDBBackup{
			Name: name, Namespace: ns,
			Spec:   api.PerconaServerMongoDBBackupSpec{ClusterName: clusterName},
			Status: api.PerconaServerMongoDBBackupStatus{State: state},
		}
	}

	tests := []struct {
		name    string
		backups []client.Object
		want    bool
	}{
		{
			name:    "no backups",
			backups: nil,
			want:    false,
		},
		{
			name:    "completed backup",
			backups: []client.Object{backup("ready", crName, api.BackupStateReady)},
			want:    false,
		},
		{
			name:    "canceled backup does not block",
			backups: []client.Object{backup("canceled", crName, api.BackupStateCanceled)},
			want:    false,
		},
		{
			name:    "canceling backup blocks",
			backups: []client.Object{backup("canceling", crName, api.BackupStateCanceling)},
			want:    true,
		},
		{
			name:    "failed backup",
			backups: []client.Object{backup("error", crName, api.BackupStateError)},
			want:    false,
		},
		{
			// A waiting backup is held before it starts: it holds no PBM lock and
			// touches no data. It can stay waiting for as long as whatever blocks
			// it persists, so treating it as running would stall Smart Update for
			// that whole time.
			name:    "waiting backup does not block",
			backups: []client.Object{backup("waiting", crName, api.BackupStateWaiting)},
			want:    false,
		},
		{
			name:    "requested backup blocks",
			backups: []client.Object{backup("requested", crName, api.BackupStateRequested)},
			want:    true,
		},
		{
			name:    "running backup blocks",
			backups: []client.Object{backup("running", crName, api.BackupStateRunning)},
			want:    true,
		},
		{
			name:    "new backup blocks",
			backups: []client.Object{backup("new", crName, api.BackupStateNew)},
			want:    true,
		},
		{
			name:    "running backup of another cluster is ignored",
			backups: []client.Object{backup("running", "other-cluster", api.BackupStateRunning)},
			want:    false,
		},
		{
			name: "waiting backups alongside a running one still block",
			backups: []client.Object{
				backup("waiting-1", crName, api.BackupStateWaiting),
				backup("waiting-2", crName, api.BackupStateWaiting),
				backup("running", crName, api.BackupStateRunning),
			},
			want: true,
		},
		{
			// The situation a ClusterSync produces: every scheduled backup piles up
			// in the waiting state while the sync holds the cluster lease.
			name: "only waiting backups do not block",
			backups: []client.Object{
				backup("waiting-1", crName, api.BackupStateWaiting),
				backup("waiting-2", crName, api.BackupStateWaiting),
				backup("waiting-3", crName, api.BackupStateWaiting),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := buildFakeClient(append([]client.Object{cr}, tt.backups...)...)

			got, err := r.isBackupRunning(ctx, cr)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
