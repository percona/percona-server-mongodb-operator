package backup

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

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

	if !cluster.Spec.UnsafeConf {
		// PBM connection is opened from the operator pod. In order to use SSL
		// certificates of the cluster, we need to copy them to operator pod.
		// This is especially important if the user passes custom config to set
		// net.tls.mode to requireTLS.

		sslSecret, err := secret(c, cluster.Namespace, cluster.Spec.Secrets.SSL)
		if err != nil {
			return nil, errors.Wrap(err, "get ssl secret")
		}

		tlsKey := sslSecret.Data["tls.key"]
		tlsCert := sslSecret.Data["tls.crt"]
		tlsPemFile := fmt.Sprintf("/tmp/%s-tls.pem", cluster.Name)
		f, err := os.OpenFile(tlsPemFile, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return nil, errors.Wrapf(err, "open %s", tlsPemFile)
		}
		defer f.Close()
		if _, err := f.Write(append(tlsKey, tlsCert...)); err != nil {
			return nil, errors.Wrapf(err, "write TLS key and certificate to %s", tlsPemFile)
		}

		caCert := sslSecret.Data["ca.crt"]
		caCertFile := fmt.Sprintf("/tmp/%s-ca.crt", cluster.Name)
		f, err = os.OpenFile(caCertFile, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return nil, errors.Wrapf(err, "open %s", caCertFile)
		}
		defer f.Close()
		if _, err := f.Write(caCert); err != nil {
			return nil, errors.Wrapf(err, "write CA certificate to %s", caCertFile)
		}

		murl += fmt.Sprintf(
			"?tls=true&tlsCertificateKeyFile=%s&tlsCAFile=%s&tlsAllowInvalidCertificates=true",
			tlsPemFile,
			caCertFile,
		)
	}

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
func (b *PBM) SetConfig(stg api.BackupStorageSpec, pitr api.PITRSpec) error {
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
			PITR: pbm.PITRConf{
				Enabled: pitr.Enabled,
			},
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

func IsPITRLock(l pbm.LockHeader) bool {
	return l.Type == pbm.CmdPITR
}

func NotJobLock(j Job) LockHeaderPredicate {
	return func(h pbm.LockHeader) bool {
		var jobCommand pbm.Command

		switch j.Type {
		case TypeBackup:
			jobCommand = pbm.CmdBackup
		case TypeRestore:
			jobCommand = pbm.CmdRestore
		case TypePITRestore:
			jobCommand = pbm.CmdPITRestore
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

var errNoOplogsForPITR = errors.New("there is no oplogs that can cover the date/time or no oplogs at all")

func (b *PBM) GetLastPITRChunk() (*pbm.PITRChunk, error) {
	nodeInfo, err := b.C.GetNodeInfo()
	if err != nil {
		return nil, errors.Wrap(err, "getting node information")
	}

	c, err := b.C.PITRLastChunkMeta(nodeInfo.SetName)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errNoOplogsForPITR
		}
		return nil, errors.Wrap(err, "getting last PITR chunk")
	}

	if c == nil {
		return nil, errNoOplogsForPITR
	}

	return c, nil
}

func (b *PBM) GetTimelinesPITR() ([]pbm.Timeline, error) {
	var (
		now       = time.Now().UTC().Unix()
		timelines [][]pbm.Timeline
	)

	shards, err := b.C.ClusterMembers(nil)
	if err != nil {
		return nil, errors.Wrap(err, "getting cluster members")
	}

	for _, s := range shards {
		rsTimelines, err := b.C.PITRGetValidTimelines(s.RS, now, nil)
		if err != nil {
			return nil, errors.Wrapf(err, "getting timelines for %s", s.RS)
		}

		timelines = append(timelines, rsTimelines)
	}

	return pbm.MergeTimelines(timelines...), nil
}

func (b *PBM) GetLatestTimelinePITR() (pbm.Timeline, error) {
	timelines, err := b.GetTimelinesPITR()
	if err != nil {
		return pbm.Timeline{}, err
	}

	if len(timelines) == 0 {
		return pbm.Timeline{}, errNoOplogsForPITR
	}

	return timelines[len(timelines)-1], nil
}

func (b *PBM) GetPITRChunkContains(unixTS int64) (*pbm.PITRChunk, error) {
	nodeInfo, err := b.C.GetNodeInfo()
	if err != nil {
		return nil, errors.Wrap(err, "getting node information")
	}

	c, err := b.C.PITRGetChunkContains(nodeInfo.SetName, primitive.Timestamp{T: uint32(unixTS)})
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errNoOplogsForPITR
		}
		return nil, errors.Wrap(err, "getting PITR chunk for ts")
	}

	if c == nil {
		return nil, errNoOplogsForPITR
	}

	return c, nil
}
