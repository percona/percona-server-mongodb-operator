package backup

import (
	"context"
	"fmt"
	"strings"

	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"

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
	C         *pbm.PBM
	k8c       client.Client
	namespace string
}

// NewPBM creates a new connection to PBM.
// It should be closed after the last use with.
func NewPBM(c client.Client, cluster *api.PerconaServerMongoDB) (*PBM, error) {
	rs := cluster.Spec.Replsets[0]

	pods := &corev1.PodList{}
	err := c.List(context.TODO(),
		pods,
		&client.ListOptions{
			Namespace: cluster.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"app.kubernetes.io/name":       "percona-server-mongodb",
				"app.kubernetes.io/instance":   cluster.Name,
				"app.kubernetes.io/replset":    rs.Name,
				"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
				"app.kubernetes.io/part-of":    "percona-server-mongodb",
			}),
		},
	)
	if err != nil {
		return nil, errors.Wrapf(err, "get pods list for replset %s", rs.Name)
	}

	usersSecretName := api.UserSecretName(cluster)
	scr, err := secret(c, cluster.Namespace, usersSecretName)
	if err != nil {
		return nil, errors.Wrap(err, "get secrets")
	}

	if len(cluster.Spec.ClusterServiceDNSSuffix) == 0 {
		cluster.Spec.ClusterServiceDNSSuffix = api.DefaultDNSSuffix
	}

	addrs, err := psmdb.GetReplsetAddrs(c, cluster, rs.Name, rs.Expose.Enabled, pods.Items)
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

	pbmc.InitLogger("", "")

	return &PBM{
		C:         pbmc,
		k8c:       c,
		namespace: cluster.Namespace,
	}, nil
}

// SetConfig sets the pbm config with storage defined in the cluster CR
// by given storageName
func (b *PBM) SetConfig(stg api.BackupStorageSpec) error {
	switch stg.Type {
	case api.BackupStorageS3:
		if stg.S3.CredentialsSecret == "" {
			return errors.New("no credentials specified for the secret name")
		}
		s3secret, err := secret(b.k8c, b.namespace, stg.S3.CredentialsSecret)
		if err != nil {
			return errors.Wrap(err, "getting s3 credentials secret name")
		}
		conf := pbm.Config{
			Storage: pbm.StorageConf{
				Type: pbm.StorageS3,
				S3: s3.Conf{
					Region:      stg.S3.Region,
					EndpointURL: stg.S3.EndpointURL,
					Bucket:      stg.S3.Bucket,
					Prefix:      stg.S3.Prefix,
					Credentials: s3.Credentials{
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
	case api.BackupStorageFilesystem:
		return errors.New("filesystem backup storage not supported yet, skipping storage name")
	default:
		return errors.New("unsupported backup storage type")
	}

	return nil
}

// Close close the PBM connection
func (b *PBM) Close() error {
	return b.C.Conn.Disconnect(context.Background())
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

type LockHeaderPredicate func(pbm.LockHeader) bool

func NotPITRLock(l pbm.LockHeader) bool {
	return l.Type != pbm.CmdPITR
}

func NotJobLock(j Job) LockHeaderPredicate {
	return func(h pbm.LockHeader) bool {
		var jobCommand pbm.Command

		switch j.Type {
		case TypeBackup:
			jobCommand = pbm.CmdBackup
		case TypeRestore:
			jobCommand = pbm.CmdRestore
		default:
			return true
		}

		return h.Type != jobCommand
	}
}

func (b *PBM) HasLocks(predicates ...LockHeaderPredicate) (bool, error) {
	locks, err := b.C.GetLocks(&pbm.LockHeader{})
	if err != nil {
		return false, errors.Wrap(err, "getting lock data")
	}

	allowedByAllPredicates := func(l pbm.LockHeader) bool {
		for _, allow := range predicates {
			if !allow(l) {
				return false
			}
		}
		return true
	}

	for _, l := range locks {
		if allowedByAllPredicates(l.LockHeader) {
			return true, nil
		}
	}

	return false, nil
}
