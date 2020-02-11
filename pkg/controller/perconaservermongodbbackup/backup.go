package perconaservermongodbbackup

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/percona/percona-server-mongodb-operator/pkg/controller/perconaservermongodb"

	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	client "sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

type Backup struct {
	pbm *pbm.PBM
	k8c client.Client
}

func (r *ReconcilePerconaServerMongoDBBackup) newBackup(cr *api.PerconaServerMongoDBBackup) (*Backup, error) {
	cluster := &api.PerconaServerMongoDB{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Spec.PSMDBCluster, Namespace: cr.Namespace}, cluster)
	if err != nil {
		return nil, errors.Wrapf(err, "get cluster %s/%s", cr.Namespace, cr.Spec.PSMDBCluster)
	}

	pods := &corev1.PodList{}
	err = r.client.List(context.TODO(),
		&client.ListOptions{
			Namespace: cr.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"app.kubernetes.io/name":       "percona-server-mongodb",
				"app.kubernetes.io/instance":   cr.Spec.PSMDBCluster,
				"app.kubernetes.io/replset":    cr.Spec.Replset,
				"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
				"app.kubernetes.io/part-of":    "percona-server-mongodb",
			}),
		},
		pods,
	)
	if err != nil {
		return nil, errors.Wrapf(err, "get pods list for replset %s", cr.Spec.Replset)
	}

	var rs *api.ReplsetSpec
	for _, r := range cluster.Spec.Replsets {
		if r != nil && r.Name == cr.Spec.Replset {
			rs = r
			break
		}
	}

	if rs == nil {
		return nil, errors.Errorf("replset %s not found", cr.Spec.Replset)
	}

	scr, err := secret(r.client, cr.Namespace, cluster.Spec.Secrets.Users)
	if err != nil {
		return nil, errors.Wrap(err, "get secrets")
	}

	addrs := perconaservermongodb.GetReplsetAddrs(r.client, cluster, rs, pods.Items)
	murl := fmt.Sprintf("mongodb://%s:%s@%s/",
		scr.Data["MONGODB_BACKUP_USER"],
		scr.Data["MONGODB_BACKUP_PASSWORD"],
		strings.Join(addrs, ","),
	)

	c, err := pbm.New(context.Background(), murl, "operator-pbm-ctl")
	if err != nil {
		return nil, errors.Wrap(err, "create PBM connection")
	}

	return &Backup{pbm: c, k8c: r.client}, nil
}

func secret(cl client.Client, namespace, secretName string) (*corev1.Secret, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
	}
	err := cl.Get(context.TODO(), types.NamespacedName{Name: secretName, Namespace: namespace}, secret)
	return secret, err
}

const (
	awsAccessKeySecretKey       = "AWS_ACCESS_KEY_ID"
	awsSecretAccessKeySecretKey = "AWS_SECRET_ACCESS_KEY"
)

func (b *Backup) SetConfig(cr *api.PerconaServerMongoDBBackup) error {
	cluster := &api.PerconaServerMongoDB{}
	err := b.k8c.Get(context.TODO(), types.NamespacedName{Name: cr.Spec.PSMDBCluster, Namespace: cr.Namespace}, cluster)
	if err != nil {
		return errors.Wrapf(err, "get cluster %s/%s", cr.Namespace, cr.Spec.PSMDBCluster)
	}

	stg, ok := cluster.Spec.Backup.Storages[cr.Spec.StorageName]
	if !ok {
		return errors.Errorf("unable to get storage '%s' at cluster '%s/%s'", cr.Spec.StorageName, cr.Namespace, cr.Spec.PSMDBCluster)
	}

	switch stg.Type {
	case pbm.StorageS3:
		if stg.S3.CredentialsSecret == "" {
			return errors.Errorf("no credentials specified for the secret name %s", cr.Spec.StorageName)
		}
		s3secret, err := secret(b.k8c, cr.Namespace, stg.S3.CredentialsSecret)
		if err != nil {
			return errors.Wrapf(err, "getting s3 credentials secret name %s", cr.Spec.StorageName)
		}
		err = b.pbm.SetConfig(pbm.Config{
			Storage: pbm.Storage{
				Type: pbm.StorageS3,
				S3: pbm.S3{
					Region:      stg.S3.Region,
					EndpointURL: stg.S3.EndpointURL,
					Bucket:      stg.S3.Bucket,
					Prefix:      stg.S3.Prefix,
					Credentials: pbm.Credentials{
						AccessKeyID:     string(s3secret.Data[awsAccessKeySecretKey]),
						SecretAccessKey: string(s3secret.Data[awsSecretAccessKeySecretKey]),
					},
				},
			},
		})
		if err != nil {
			return errors.Wrap(err, "write config")
		}
	case pbm.StorageFilesystem:
		return errors.Errorf("filesystem backup storage not supported yet, skipping storage name: %s", cr.Spec.StorageName)
	default:
		return errors.Errorf("unsupported backup storage type: %s", cr.Spec.StorageName)
	}

	return nil
}

func (b *Backup) Start(cr *api.PerconaServerMongoDBBackup) *api.PerconaServerMongoDBBackupStatus {
	backupStatus := psmdbv1.PerconaServerMongoDBBackupStatus{
		StorageName: cr.Spec.StorageName,
		PBMname:     time.Now().UTC().Format(time.RFC3339),
		LastTransition: &metav1.Time{
			Time: time.Unix(time.Now().Unix(), 0),
		},
	}

	err := b.pbm.SendCmd(pbm.Cmd{
		Cmd: pbm.CmdBackup,
		Backup: pbm.BackupCmd{
			Name:        backupStatus.PBMname,
			Compression: pbm.CompressionTypeGZIP,
		},
	})
	if err != nil {
		backupStatus.State = psmdbv1.StateRejected
		backupStatus.Error = err.Error()
		return &backupStatus
	}

	backupStatus.State = psmdbv1.StateRequested
	return &backupStatus
}

func (b *Backup) Status(cr *api.PerconaServerMongoDBBackup) (*api.PerconaServerMongoDBBackupStatus, error) {
	meta, err := b.pbm.GetBackupMeta(cr.Status.PBMname)
	if err != nil {
		return nil, errors.Wrap(err, "get pbm backup meta")
	}
	if meta == nil {
		return nil, nil
	}

	status := cr.Status

	if meta.StartTS > 0 {
		status.StartAt = &metav1.Time{
			Time: time.Unix(meta.StartTS, 0),
		}
	}

	switch meta.Status {
	case pbm.StatusError:
		status.State = api.StateError
		status.Error = meta.Error
	case pbm.StatusDone:
		status.State = api.StateError
	default:
		status.State = api.StateRunning
	}

	status.LastTransition = &metav1.Time{
		Time: time.Unix(meta.LastTransitionTS, 0),
	}

	switch meta.Store.Type {
	case pbm.StorageS3:
		status.Destination = "s3://"
		if meta.Store.S3.EndpointURL != "" {
			status.Destination += meta.Store.S3.EndpointURL + "/"
		}
		status.Destination += meta.Store.S3.Bucket
		if meta.Store.S3.Prefix != "" {
			status.Destination += "/" + meta.Store.S3.Prefix
		}
	case pbm.StorageFilesystem:
		status.Destination = meta.Store.Filesystem.Path
	}

	return &status, nil
}

func (b *Backup) Close() error {
	return b.pbm.Conn.Disconnect(context.Background())
}
