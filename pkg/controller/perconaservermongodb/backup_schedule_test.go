package perconaservermongodb

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func TestMissedCronTick(t *testing.T) {
	t.Parallel()

	last := time.Date(2026, 7, 12, 1, 0, 0, 0, time.UTC)
	now := time.Date(2026, 7, 13, 1, 5, 0, 0, time.UTC)

	tests := []struct {
		name     string
		schedule string
		last     time.Time
		now      time.Time
		want     bool
	}{
		{name: "zero last", schedule: "0 1 * * *", want: false},
		{name: "missed daily 01:00", schedule: "0 1 * * *", last: last, now: now, want: true},
		{name: "already ran today", schedule: "0 1 * * *", last: time.Date(2026, 7, 13, 1, 0, 2, 0, time.UTC), now: now, want: false},
		{name: "still before next tick", schedule: "0 1 * * *", last: last, now: time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := missedCronTick(tt.schedule, tt.last, tt.now)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCatchUpMissedScheduledBackup(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, api.SchemeBuilder.AddToScheme(scheme))

	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Namespace: "db"},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: "1.23.0",
			Backup: api.BackupSpec{
				Enabled: true,
			},
		},
	}
	task := api.BackupTaskSpec{
		Name:        "daily",
		Enabled:     true,
		Schedule:    "0 1 * * *",
		StorageName: "s3-us-west",
	}

	old := &api.PerconaServerMongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "cron-old",
			Namespace:         "db",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-26 * time.Hour)),
			Labels:            naming.ScheduledBackupLabels(cr, &task),
		},
		Spec: api.PerconaServerMongoDBBackupSpec{
			ClusterName: cr.Name,
			StorageName: task.StorageName,
		},
		Status: api.PerconaServerMongoDBBackupStatus{State: api.BackupStateReady},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr, old).Build()
	r := &ReconcilePerconaServerMongoDB{
		client: cl,
		scheme: scheme,
		crons:  NewCronRegistry(),
	}
	t.Cleanup(func() { r.crons.crons.Stop() })

	require.NoError(t, r.createOrUpdateBackupTask(t.Context(), cr, task))

	list := api.PerconaServerMongoDBBackupList{}
	require.NoError(t, cl.List(t.Context(), &list, &client.ListOptions{Namespace: "db"}))
	assert.GreaterOrEqual(t, len(list.Items), 2, "expected a catch-up PerconaServerMongoDBBackup after a missed daily tick")
}

func TestCatchUpMissedScheduledBackupSkipsFirstInstall(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, api.SchemeBuilder.AddToScheme(scheme))

	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Namespace: "db"},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: "1.23.0",
			Backup:    api.BackupSpec{Enabled: true},
		},
	}
	task := api.BackupTaskSpec{
		Name:        "daily",
		Enabled:     true,
		Schedule:    "0 1 * * *",
		StorageName: "s3-us-west",
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr).Build()
	r := &ReconcilePerconaServerMongoDB{
		client: cl,
		scheme: scheme,
		crons:  NewCronRegistry(),
	}
	t.Cleanup(func() { r.crons.crons.Stop() })

	require.NoError(t, r.createOrUpdateBackupTask(t.Context(), cr, task))

	list := api.PerconaServerMongoDBBackupList{}
	require.NoError(t, cl.List(t.Context(), &list, &client.ListOptions{Namespace: "db"}))
	assert.Empty(t, list.Items, "first install must not immediately create a backup")
}
