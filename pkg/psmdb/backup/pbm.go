package backup

import (
	"context"
	"fmt"
	"strings"

	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	client "sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

const (
	agentContainerName          = "backup-agent"
	awsAccessKeySecretKey       = "AWS_ACCESS_KEY_ID"
	awsSecretAccessKeySecretKey = "AWS_SECRET_ACCESS_KEY"
)

type PBM struct {
	C   *pbm.PBM
	k8c client.Client
}

// NewPBM creates a new connection to PBM.
// It should be closed after the last use with.
func NewPBM(c client.Client, clusterName, replset, namespace string) (*PBM, error) {
	cluster := &api.PerconaServerMongoDB{}
	err := c.Get(context.TODO(), types.NamespacedName{Name: clusterName, Namespace: namespace}, cluster)
	if err != nil {
		return nil, errors.Wrapf(err, "get cluster %s/%s", namespace, clusterName)
	}

	// if replset is not defined than we choose the first one
	rsName := replset
	var rs *api.ReplsetSpec
	for _, r := range cluster.Spec.Replsets {
		if replset == "" || r != nil && r.Name == replset {
			rsName = r.Name
			rs = r
			break
		}
	}
	if rs == nil {
		return nil, errors.Errorf("replset %s not found", replset)
	}

	pods := &corev1.PodList{}
	err = c.List(context.TODO(),
		&client.ListOptions{
			Namespace: namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"app.kubernetes.io/name":       "percona-server-mongodb",
				"app.kubernetes.io/instance":   clusterName,
				"app.kubernetes.io/replset":    rsName,
				"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
				"app.kubernetes.io/part-of":    "percona-server-mongodb",
			}),
		},
		pods,
	)
	if err != nil {
		return nil, errors.Wrapf(err, "get pods list for replset %s", replset)
	}

	scr, err := secret(c, namespace, cluster.Spec.Secrets.Users)
	if err != nil {
		return nil, errors.Wrap(err, "get secrets")
	}

	if len(cluster.Spec.ClusterServiceDNSSuffix) == 0 {
		cluster.Spec.ClusterServiceDNSSuffix = "svc.cluster.local"
	}

	addrs, err := psmdb.GetReplsetAddrs(c, cluster, rs, pods.Items)
	if err != nil {
		return nil, errors.Wrap(err, "get mongo addr")
	}
	murl := fmt.Sprintf("mongodb://%s:%s@%s/",
		scr.Data["MONGODB_BACKUP_USER"],
		scr.Data["MONGODB_BACKUP_PASSWORD"],
		strings.Join(addrs, ","),
	)

	pbmc, err := pbm.New(context.Background(), murl, "operator-pbm-ctl")
	if err != nil {
		return nil, errors.Wrapf(err, "create PBM connection to %s", strings.Join(addrs, ","))
	}

	return &PBM{C: pbmc, k8c: c}, nil
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

func (b *PBM) SetConfig(cr *api.PerconaServerMongoDBBackup) error {
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
		conf := pbm.Config{
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
		}
		err = b.C.SetConfig(conf)
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

func (b *PBM) Close() error {
	return b.C.Conn.Disconnect(context.Background())
}
