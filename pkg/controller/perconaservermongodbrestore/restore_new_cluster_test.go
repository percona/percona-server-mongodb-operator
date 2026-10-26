package perconaservermongodbrestore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/mongo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	pbmErrors "github.com/percona/percona-backup-mongodb/pbm/errors"
	pbmVersion "github.com/percona/percona-backup-mongodb/pbm/version"

	fakeClientcmd "github.com/percona/percona-server-mongodb-operator/clientcmd/fake"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

var (
	backupMetaNotFound = errors.Wrap(errors.Wrap(pbmErrors.ErrNotFound, "get backup meta"), "validate backup in metadata")
	resyncNotStarted   = errors.Wrap(errors.Wrap(backup.ErrResyncNotStarted, "wait for resync to start"), "wait for resync")
)

func newClusterRestoreObjects(t *testing.T) (*psmdbv1.PerconaServerMongoDB, *psmdbv1.PerconaServerMongoDBBackup, *psmdbv1.PerconaServerMongoDBRestore, *corev1.Secret) {
	t.Helper()

	const (
		ns          = "new-cluster-restore"
		clusterName = "my-cluster"
		storageName = "my-storage"
		secretName  = "my-storage-secret"
	)

	cluster := readDefaultCluster(t, clusterName, ns)
	cluster.Spec.Sharding.Enabled = false
	cluster.Spec.Backup.Storages = map[string]psmdbv1.BackupStorageSpec{
		storageName: {
			Type: psmdbv1.BackupStorageS3,
			S3: psmdbv1.BackupStorageS3Spec{
				Bucket:            "some-bucket",
				Region:            "some-region",
				EndpointURL:       "some-endpoint",
				CredentialsSecret: secretName,
			},
		},
	}
	cluster.Status.BackupVersion = pbmVersion.Current().Version

	bcp := readDefaultBackup(t, "my-backup", ns)
	bcp.Spec.ClusterName = clusterName
	bcp.Spec.StorageName = storageName
	bcp.Status = psmdbv1.PerconaServerMongoDBBackupStatus{
		Type:        defs.LogicalBackup,
		State:       psmdbv1.BackupStateReady,
		StorageName: storageName,
		PBMname:     "2026-10-26T16:00:00Z",
	}

	restore := readDefaultRestore(t, "my-restore", ns)
	restore.Spec.ClusterName = clusterName
	restore.Spec.BackupName = bcp.Name

	secret := &corev1.Secret{Name: secretName, Namespace: ns}

	return cluster, bcp, restore, secret
}

// k8sVersionClient serves /version so that version.Server works without an API server.
func k8sVersionClient(t *testing.T) *fakeClientcmd.Client {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/version" {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write([]byte(`{"major":"1","minor":"31","gitVersion":"v1.31.0"}`))
	}))
	t.Cleanup(srv.Close)

	rc, err := rest.RESTClientFor(&rest.Config{
		Host: srv.URL,
		ContentConfig: rest.ContentConfig{
			GroupVersion:         &metav1.SchemeGroupVersion,
			NegotiatedSerializer: scheme.Codecs.WithoutConversion(),
		},
	})
	require.NoError(t, err)

	return &fakeClientcmd.Client{RESTFunc: func() rest.Interface { return rc }}
}

func newClusterRestoreReconciler(t *testing.T, objs ...client.Object) (*ReconcilePerconaServerMongoDBRestore, *MockPBM) {
	t.Helper()

	pbmc := NewMockPBM(gomock.NewController(t))
	pbmc.EXPECT().HasLocks(gomock.Any(), gomock.Any()).Return(false, nil).AnyTimes()
	pbmc.EXPECT().Close(gomock.Any()).Return(nil).AnyTimes()

	r := fakeReconciler(objs...)
	r.clientcmd = k8sVersionClient(t)
	r.reconcileIn = 5 * time.Second
	r.newPBMFunc = func(context.Context, client.Client, *psmdbv1.PerconaServerMongoDB) (backup.PBM, error) {
		return pbmc, nil
	}

	return r, pbmc
}

func reconcileRestore(t *testing.T, r *ReconcilePerconaServerMongoDBRestore, restore *psmdbv1.PerconaServerMongoDBRestore) (*psmdbv1.PerconaServerMongoDBRestore, reconcile.Result, error) {
	t.Helper()

	res, err := r.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(restore)})

	got := new(psmdbv1.PerconaServerMongoDBRestore)
	require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(restore), got))

	return got, res, err
}

func expectLogicalRestoreStart(pbmc *MockPBM) {
	pbmc.EXPECT().SetConfigVar(gomock.Any(), "pitr.enabled", "false").Return(nil)
	pbmc.EXPECT().SendCmd(gomock.Any(), gomock.Any()).Return(nil)
}

func TestReconcileWaitsForBackupVersion(t *testing.T) {
	t.Run("logical restore waits and starts once the version is set", func(t *testing.T) {
		cluster, bcp, restore, secret := newClusterRestoreObjects(t)
		cluster.Status.BackupVersion = ""

		r, pbmc := newClusterRestoreReconciler(t, cluster, bcp, restore, secret)

		// ValidateBackup is not expected, the restore must not reach PBM validation yet.
		got, res, err := reconcileRestore(t, r, restore)
		require.NoError(t, err)
		assert.Equal(t, psmdbv1.RestoreStateNew, got.Status.State)
		assert.Empty(t, got.Status.Error)
		assert.Equal(t, r.reconcileIn, res.RequeueAfter)

		c := new(psmdbv1.PerconaServerMongoDB)
		require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(cluster), c))
		c.Status.BackupVersion = pbmVersion.Current().Version
		require.NoError(t, r.client.Status().Update(t.Context(), c))

		pbmc.EXPECT().ValidateBackup(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		expectLogicalRestoreStart(pbmc)

		got, _, err = reconcileRestore(t, r, restore)
		require.NoError(t, err)
		assert.Equal(t, psmdbv1.RestoreStateRequested, got.Status.State)
		assert.NotEmpty(t, got.Status.PBMname)
		assert.Empty(t, got.Status.Error)
	})

	t.Run("physical restore fails as before", func(t *testing.T) {
		cluster, bcp, restore, secret := newClusterRestoreObjects(t)
		cluster.Status.BackupVersion = ""
		bcp.Spec.Type = defs.PhysicalBackup
		bcp.Status.Type = defs.PhysicalBackup

		r, _ := newClusterRestoreReconciler(t, cluster, bcp, restore, secret)

		got, _, err := reconcileRestore(t, r, restore)
		require.ErrorContains(t, err, "parse status.backupVersion")
		assert.Equal(t, psmdbv1.RestoreStateError, got.Status.State)
		assert.Contains(t, got.Status.Error, "parse status.backupVersion")
	})
}

func TestReconcileResyncNotStarted(t *testing.T) {
	t.Run("backupName on a cluster without PBM config waits and resyncs again", func(t *testing.T) {
		cluster, bcp, restore, secret := newClusterRestoreObjects(t)

		r, pbmc := newClusterRestoreReconciler(t, cluster, bcp, restore, secret)

		gomock.InOrder(
			pbmc.EXPECT().ValidateBackup(gomock.Any(), gomock.Any(), gomock.Any()).Return(backupMetaNotFound),
			pbmc.EXPECT().GetConfig(gomock.Any()).Return(nil, mongo.ErrNoDocuments),
			pbmc.EXPECT().GetNSetConfig(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil),
			pbmc.EXPECT().ResyncMainStorageAndWait(gomock.Any()).Return(resyncNotStarted),
		)

		got, res, err := reconcileRestore(t, r, restore)
		require.NoError(t, err)
		assert.Equal(t, psmdbv1.RestoreStateNew, got.Status.State)
		assert.Empty(t, got.Status.Error)
		assert.Equal(t, r.reconcileIn, res.RequeueAfter)

		gomock.InOrder(
			pbmc.EXPECT().ValidateBackup(gomock.Any(), gomock.Any(), gomock.Any()).Return(backupMetaNotFound),
			pbmc.EXPECT().GetConfig(gomock.Any()).Return(&config.Config{}, nil),
			pbmc.EXPECT().ResyncMainStorageAndWait(gomock.Any()).Return(nil),
			pbmc.EXPECT().ValidateBackup(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil),
		)

		got, _, err = reconcileRestore(t, r, restore)
		require.NoError(t, err)
		assert.Equal(t, psmdbv1.RestoreStateNew, got.Status.State)
		assert.Empty(t, got.Status.Error)

		pbmc.EXPECT().ValidateBackup(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		expectLogicalRestoreStart(pbmc)

		got, _, err = reconcileRestore(t, r, restore)
		require.NoError(t, err)
		assert.Equal(t, psmdbv1.RestoreStateRequested, got.Status.State)
	})

	t.Run("backupSource on a cluster without PBM config waits and resyncs again", func(t *testing.T) {
		cluster, bcp, restore, secret := newClusterRestoreObjects(t)
		restore.Spec.BackupName = ""
		restore.Spec.BackupSource = &psmdbv1.PerconaServerMongoDBBackupStatus{
			Type:        defs.LogicalBackup,
			Destination: "s3://some-bucket/" + bcp.Status.PBMname,
			S3: &psmdbv1.BackupStorageS3Spec{
				Bucket:            "some-bucket",
				Region:            "some-region",
				EndpointURL:       "some-endpoint",
				CredentialsSecret: secret.Name,
			},
		}
		profile := restore.Name + "-backup-source"

		r, pbmc := newClusterRestoreReconciler(t, cluster, restore, secret)

		gomock.InOrder(
			pbmc.EXPECT().ValidateBackup(gomock.Any(), gomock.Any(), gomock.Any()).Return(backupMetaNotFound),
			pbmc.EXPECT().GetConfig(gomock.Any()).Return(nil, mongo.ErrNoDocuments),
			pbmc.EXPECT().SetConfig(gomock.Any(), gomock.Any()).Return(nil),
			pbmc.EXPECT().ResyncMainStorageAndWait(gomock.Any()).Return(resyncNotStarted),
		)

		got, _, err := reconcileRestore(t, r, restore)
		require.NoError(t, err)
		assert.Equal(t, psmdbv1.RestoreStateNew, got.Status.State)
		assert.Empty(t, got.Status.Error)

		gomock.InOrder(
			pbmc.EXPECT().ValidateBackup(gomock.Any(), gomock.Any(), gomock.Any()).Return(backupMetaNotFound),
			pbmc.EXPECT().GetConfig(gomock.Any()).Return(&config.Config{}, nil),
			pbmc.EXPECT().AddProfile(gomock.Any(), gomock.Any(), gomock.Any(), profile, gomock.Any()).Return(nil),
			pbmc.EXPECT().ResyncProfileAndWait(gomock.Any(), profile).Return(nil),
			pbmc.EXPECT().ValidateBackup(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil),
		)

		got, _, err = reconcileRestore(t, r, restore)
		require.NoError(t, err)
		assert.Equal(t, psmdbv1.RestoreStateNew, got.Status.State)
		assert.Empty(t, got.Status.Error)

		pbmc.EXPECT().ValidateBackup(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		expectLogicalRestoreStart(pbmc)

		got, _, err = reconcileRestore(t, r, restore)
		require.NoError(t, err)
		assert.Equal(t, psmdbv1.RestoreStateRequested, got.Status.State)
	})

	t.Run("cluster with PBM configured before fails", func(t *testing.T) {
		cluster, bcp, restore, secret := newClusterRestoreObjects(t)
		cluster.Status.BackupConfigHash = "some-hash"

		r, pbmc := newClusterRestoreReconciler(t, cluster, bcp, restore, secret)

		gomock.InOrder(
			pbmc.EXPECT().ValidateBackup(gomock.Any(), gomock.Any(), gomock.Any()).Return(backupMetaNotFound),
			pbmc.EXPECT().GetConfig(gomock.Any()).Return(&config.Config{}, nil),
			pbmc.EXPECT().ResyncMainStorageAndWait(gomock.Any()).Return(resyncNotStarted),
		)

		got, _, err := reconcileRestore(t, r, restore)
		require.ErrorIs(t, err, backup.ErrResyncNotStarted)
		assert.Equal(t, psmdbv1.RestoreStateError, got.Status.State)
		assert.Equal(t, "failed to validate backup: validate backup in metadata: get backup meta: not found", got.Status.Error)
	})

	t.Run("cluster without PBM config fails on other resync errors", func(t *testing.T) {
		cluster, bcp, restore, secret := newClusterRestoreObjects(t)

		r, pbmc := newClusterRestoreReconciler(t, cluster, bcp, restore, secret)

		gomock.InOrder(
			pbmc.EXPECT().ValidateBackup(gomock.Any(), gomock.Any(), gomock.Any()).Return(backupMetaNotFound),
			pbmc.EXPECT().GetConfig(gomock.Any()).Return(nil, mongo.ErrNoDocuments),
			pbmc.EXPECT().GetNSetConfig(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("boom")),
		)

		got, _, err := reconcileRestore(t, r, restore)
		require.ErrorContains(t, err, "boom")
		assert.Equal(t, psmdbv1.RestoreStateError, got.Status.State)
	})

	t.Run("physical restore on a cluster without PBM config fails as before", func(t *testing.T) {
		cluster, bcp, restore, secret := newClusterRestoreObjects(t)
		bcp.Spec.Type = defs.PhysicalBackup
		bcp.Status.Type = defs.PhysicalBackup

		r, pbmc := newClusterRestoreReconciler(t, cluster, bcp, restore, secret)

		// GetConfig and GetNSetConfig are not expected, physical restores don't touch the PBM config here.
		gomock.InOrder(
			pbmc.EXPECT().ValidateBackup(gomock.Any(), gomock.Any(), gomock.Any()).Return(backupMetaNotFound),
			pbmc.EXPECT().ResyncMainStorageAndWait(gomock.Any()).Return(resyncNotStarted),
		)

		got, _, err := reconcileRestore(t, r, restore)
		require.ErrorIs(t, err, backup.ErrResyncNotStarted)
		assert.Equal(t, psmdbv1.RestoreStateError, got.Status.State)
		assert.Equal(t, "failed to validate backup: validate backup in metadata: get backup meta: not found", got.Status.Error)
	})
}

func TestResyncStorageSetsMissingConfig(t *testing.T) {
	tests := map[string]struct {
		backupType    defs.BackupType
		getConfigErr  error
		wantGetConfig bool
		wantSetConfig bool
	}{
		"logical, no PBM config":            {backupType: defs.LogicalBackup, getConfigErr: mongo.ErrNoDocuments, wantGetConfig: true, wantSetConfig: true},
		"logical, PBM config exists":        {backupType: defs.LogicalBackup, wantGetConfig: true},
		"logical, PBM config can't be read": {backupType: defs.LogicalBackup, getConfigErr: errors.New("boom"), wantGetConfig: true},
		"physical":                          {backupType: defs.PhysicalBackup},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster, bcp, restore, secret := newClusterRestoreObjects(t)
			bcp.Spec.Type = tt.backupType
			bcp.Status.Type = tt.backupType

			r, pbmc := newClusterRestoreReconciler(t, cluster, bcp, restore, secret)

			if tt.wantGetConfig {
				pbmc.EXPECT().GetConfig(gomock.Any()).Return(&config.Config{}, tt.getConfigErr)
			}
			if tt.wantSetConfig {
				pbmc.EXPECT().GetNSetConfig(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			}
			pbmc.EXPECT().ResyncMainStorageAndWait(gomock.Any()).Return(nil)

			require.NoError(t, r.resyncStorage(t.Context(), cluster, restore))
		})
	}
}

func TestValidateBackupVersion(t *testing.T) {
	cluster, bcp, restore, secret := newClusterRestoreObjects(t)
	cluster.Status.BackupVersion = ""

	t.Run("logical backup", func(t *testing.T) {
		r := fakeReconciler(cluster.DeepCopy(), bcp.DeepCopy(), restore.DeepCopy(), secret.DeepCopy())

		err := r.validate(t.Context(), restore, cluster)
		assert.ErrorIs(t, err, errWaitingPBMVersion)
	})

	t.Run("logical backup with backups disabled", func(t *testing.T) {
		r := fakeReconciler(cluster.DeepCopy(), bcp.DeepCopy(), restore.DeepCopy(), secret.DeepCopy())

		disabled := cluster.DeepCopy()
		disabled.Spec.Backup.Enabled = false

		err := r.validate(t.Context(), restore, disabled)
		assert.NotErrorIs(t, err, errWaitingPBMVersion)
		assert.ErrorContains(t, err, "parse status.backupVersion")
	})

	t.Run("physical backup", func(t *testing.T) {
		physical := bcp.DeepCopy()
		physical.Spec.Type = defs.PhysicalBackup
		physical.Status.Type = defs.PhysicalBackup

		r := fakeReconciler(cluster.DeepCopy(), physical, restore.DeepCopy(), secret.DeepCopy())

		err := r.validate(t.Context(), restore, cluster)
		assert.NotErrorIs(t, err, errWaitingPBMVersion)
		assert.ErrorContains(t, err, "parse status.backupVersion")
	})
}

func TestPBMNotConfiguredYet(t *testing.T) {
	tests := map[string]struct {
		crVersion  string
		noStorages bool
		configHash string
		want       bool
	}{
		"new cluster":             {crVersion: "1.24.0", want: true},
		"configured cluster":      {crVersion: "1.24.0", configHash: "some-hash"},
		"no backup storages":      {crVersion: "1.24.0", noStorages: true},
		"crVersion before 1.20.0": {crVersion: "1.19.1"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cluster, _, _, _ := newClusterRestoreObjects(t)
			cluster.Spec.CRVersion = tt.crVersion
			cluster.Status.BackupConfigHash = tt.configHash
			if tt.noStorages {
				cluster.Spec.Backup.Storages = nil
			}

			assert.Equal(t, tt.want, pbmNotConfiguredYet(cluster))
		})
	}
}
