package perconaservermongodb

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func TestIsBackupRunning(t *testing.T) {
	ctx := context.Background()

	const (
		crName = "some-name"
		ns     = "psmdb"
	)

	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: ns},
	}

	backup := func(name, clusterName string, state api.BackupState) *api.PerconaServerMongoDBBackup {
		return &api.PerconaServerMongoDBBackup{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       api.PerconaServerMongoDBBackupSpec{ClusterName: clusterName},
			Status:     api.PerconaServerMongoDBBackupStatus{State: state},
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
			if err != nil {
				t.Fatalf("isBackupRunning() returned an error: %v", err)
			}
			if got != tt.want {
				t.Errorf("isBackupRunning() = %v, want %v", got, tt.want)
			}
		})
	}
}
