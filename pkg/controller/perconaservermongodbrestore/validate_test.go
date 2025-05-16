package perconaservermongodbrestore

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/controller/common"
	fakeBackup "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup/fake"
)

func TestValidate(t *testing.T) {
	ctx := context.Background()

	ns := "validate"
	clusterName := ns + "-cr"
	restoreName := ns + "-restore"
	backupName := ns + "-backup"
	secretName := ns + "-secret"
	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: ns,
		},
		Data: map[string][]byte{},
	}

	storageName := ns + "-stg"

	cluster := readDefaultCluster(t, clusterName, ns)
	cluster.Spec.Backup.Storages = map[string]psmdbv1.BackupStorageSpec{
		storageName: {
			Type: psmdbv1.BackupStorageS3,
			S3: psmdbv1.BackupStorageS3Spec{
				Bucket:            "some-bucket",
				Prefix:            "some-prefix",
				Region:            "some-region",
				EndpointURL:       "some-endpoint",
				CredentialsSecret: secretName,
			},
		},
	}
	bcp := readDefaultBackup(t, backupName, ns)
	bcp.Status.State = psmdbv1.BackupStateReady
	bcp.Spec.ClusterName = clusterName
	bcp.Spec.StorageName = storageName
	cr := readDefaultRestore(t, restoreName, ns)
	cr.Spec.BackupName = backupName

	tests := []struct {
		name    string
		cr      *psmdbv1.PerconaServerMongoDBRestore
		cluster *psmdbv1.PerconaServerMongoDB
		obj     []client.Object

		expectedErr string
	}{
		{
			"s3 success",
			cr.DeepCopy(),
			cluster.DeepCopy(),
			[]client.Object{bcp.DeepCopy(), secret.DeepCopy()},
			"",
		},
		{
			"azure success",
			cr.DeepCopy(),
			updateObj(t, cluster.DeepCopy(), func(cluster *psmdbv1.PerconaServerMongoDB) {
				cluster.Spec.Backup.Storages = map[string]psmdbv1.BackupStorageSpec{
					storageName: {
						Type: psmdbv1.BackupStorageAzure,
						Azure: psmdbv1.BackupStorageAzureSpec{
							Container:         "some-container",
							Prefix:            "some-prefix",
							CredentialsSecret: secretName,
							EndpointURL:       "some-endpoint",
						},
					},
				}
			}),
			[]client.Object{bcp.DeepCopy(), secret.DeepCopy()},
			"",
		},
		{
			"unmanaged cluster",
			cr.DeepCopy(),
			updateObj(t, cluster.DeepCopy(), func(cluster *psmdbv1.PerconaServerMongoDB) {
				cluster.Spec.Unmanaged = true
			}),
			[]client.Object{bcp.DeepCopy(), secret.DeepCopy()},
			"cluster is unmanaged",
		},
		{
			"s3 no secret",
			cr.DeepCopy(),
			cluster.DeepCopy(),
			[]client.Object{bcp.DeepCopy()},
			"get pbm config: get storage config: get s3 config: get s3 credentials secret: secrets \"validate-secret\" not found",
		},
		{
			"azure no secret",
			cr.DeepCopy(),
			updateObj(t, cluster.DeepCopy(), func(cluster *psmdbv1.PerconaServerMongoDB) {
				cluster.Spec.Backup.Storages = map[string]psmdbv1.BackupStorageSpec{
					storageName: {
						Type: psmdbv1.BackupStorageAzure,
						Azure: psmdbv1.BackupStorageAzureSpec{
							Container:         "some-container",
							Prefix:            "some-prefix",
							CredentialsSecret: secretName,
							EndpointURL:       "some-endpoint",
						},
					},
				}
			}),
			[]client.Object{bcp.DeepCopy()},
			"get pbm config: get storage config: get azure config: get azure credentials secret: secrets \"validate-secret\" not found",
		},
		{
			"no backup",
			cr.DeepCopy(),
			cluster.DeepCopy(),
			[]client.Object{secret.DeepCopy()},
			"get backup: perconaservermongodbbackups.psmdb.percona.com \"validate-backup\" not found",
		},
		{
			"no storage",
			cr.DeepCopy(),
			updateObj(t, cluster.DeepCopy(), func(cluster *psmdbv1.PerconaServerMongoDB) {
				cluster.Spec.Backup.Storages = nil
			}),
			[]client.Object{bcp.DeepCopy(), secret.DeepCopy()},
			"get storage: unable to get storage 'validate-stg'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := []client.Object{tt.cr, tt.cluster}
			obj = append(obj, tt.obj...)
			r := fakeReconciler(obj...)
			err := r.validate(ctx, tt.cr, tt.cluster)
			if err != nil && err.Error() != tt.expectedErr || err == nil && tt.expectedErr != "" {
				t.Fatal("Unexpected err: ", err, "; expected: ", tt.expectedErr)
			}
		})
	}
}

func fakeReconciler(objs ...client.Object) *ReconcilePerconaServerMongoDBRestore {
	s := scheme.Scheme

	s.AddKnownTypes(psmdbv1.SchemeGroupVersion,
		new(psmdbv1.PerconaServerMongoDB),
		new(psmdbv1.PerconaServerMongoDBBackup),
		new(psmdbv1.PerconaServerMongoDBBackupList),
		new(psmdbv1.PerconaServerMongoDBRestore),
		new(psmdbv1.PerconaServerMongoDBRestoreList),
	)

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithStatusSubresource(objs...).Build()

	return &ReconcilePerconaServerMongoDBRestore{
		CommonReconciler: common.New(cl, s, fakeBackup.NewPBM, nil),
		client:           cl,
	}
}
