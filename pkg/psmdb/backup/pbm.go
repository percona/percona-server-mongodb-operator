package backup

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"

	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	client "sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

const (
	agentContainerName               = "backup-agent"
	awsAccessKeySecretKey            = "AWS_ACCESS_KEY_ID"
	awsSecretAccessKeySecretKey      = "AWS_SECRET_ACCESS_KEY"
	azureStorageAccountNameSecretKey = "AZURE_STORAGE_ACCOUNT_NAME"
	azureStorageAccountKeySecretKey  = "AZURE_STORAGE_ACCOUNT_KEY"
)

type PBM struct {
	C         *pbm.PBM
	k8c       client.Client
	namespace string
}

// NewPBM creates a new connection to PBM.
// It should be closed after the last use with.
func NewPBM(ctx context.Context, c client.Client, cluster *api.PerconaServerMongoDB) (*PBM, error) {
	rs := cluster.Spec.Replsets[0]

	pods, err := psmdb.GetRSPods(ctx, c, cluster, rs.Name)
	if err != nil {
		return nil, errors.Wrapf(err, "get pods list for replset %s", rs.Name)
	}

	usersSecretName := api.UserSecretName(cluster)
	scr, err := secret(ctx, c, cluster.Namespace, usersSecretName)
	if err != nil {
		return nil, errors.Wrap(err, "get secrets")
	}

	if len(cluster.Spec.ClusterServiceDNSSuffix) == 0 {
		cluster.Spec.ClusterServiceDNSSuffix = api.DefaultDNSSuffix
	}

	addrs, err := psmdb.GetReplsetAddrs(ctx, c, cluster, rs.Name, rs.Expose.Enabled, pods.Items)
	if err != nil {
		return nil, errors.Wrap(err, "get mongo addr")
	}

	murl := fmt.Sprintf("mongodb://%s:%s@%s/",
		scr.Data["MONGODB_BACKUP_USER"],
		scr.Data["MONGODB_BACKUP_PASSWORD"],
		strings.Join(addrs, ","),
	)

	pbmc, err := pbm.New(ctx, murl, "operator-pbm-ctl")
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

// GetPriorities returns priorities to be used in PBM config.
func (b *PBM) GetPriorities(ctx context.Context, k8sclient client.Client, cluster *api.PerconaServerMongoDB) (map[string]float64, error) {
	priorities := make(map[string]float64)

	usersSecret := corev1.Secret{}
	err := k8sclient.Get(
		ctx,
		types.NamespacedName{Name: cluster.Spec.Secrets.Users, Namespace: cluster.Namespace},
		&usersSecret,
	)
	if err != nil {
		return priorities, errors.Wrap(err, "get users secret")
	}

	c := psmdb.Credentials{
		Username: string(usersSecret.Data["MONGODB_BACKUP_USER"]),
		Password: string(usersSecret.Data["MONGODB_BACKUP_PASSWORD"]),
	}

	for _, rs := range cluster.Spec.Replsets {
		// PBM selects nodes with higher priority, so we're assigning 0.5 to
		// external nodes to run backups on nodes in the cluster.
		for _, extNode := range rs.ExternalNodes {
			priorities[extNode.HostPort()] = 0.5
		}

		cli, err := psmdb.MongoClient(ctx, k8sclient, cluster, *rs, c)
		if err != nil {
			return priorities, errors.Wrap(err, "get mongo client")
		}

		// If you explicitly set a subset of the replset nodes in the config,
		// the remaining nodes will be automatically assigned priority 1.0,
		// including the primary. That's why, we need to get primary nodes and
		// set them in the config.
		primary, err := psmdb.GetPrimaryPod(ctx, cli)
		if err != nil {
			return priorities, errors.Wrap(err, "get primary pod")
		}
		priorities[primary] = 0.5
	}

	return priorities, nil
}

// SetConfig sets the pbm config with storage defined in the cluster CR
// by given storageName
func (b *PBM) SetConfig(ctx context.Context, stg api.BackupStorageSpec, pitr api.PITRSpec, priority map[string]float64) error {
	conf := pbm.Config{
		PITR: pbm.PITRConf{
			Enabled: pitr.Enabled,
		},
		Backup: pbm.BackupConf{
			Priority: priority,
		},
	}

	switch stg.Type {
	case api.BackupStorageS3:
		conf.Storage = pbm.StorageConf{
			Type: pbm.StorageS3,
			S3: s3.Conf{
				Region:                stg.S3.Region,
				EndpointURL:           stg.S3.EndpointURL,
				Bucket:                stg.S3.Bucket,
				Prefix:                stg.S3.Prefix,
				UploadPartSize:        stg.S3.UploadPartSize,
				MaxUploadParts:        stg.S3.MaxUploadParts,
				StorageClass:          stg.S3.StorageClass,
				InsecureSkipTLSVerify: stg.S3.InsecureSkipTLSVerify,
			},
		}

		if len(stg.S3.CredentialsSecret) != 0 {
			s3secret, err := secret(ctx, b.k8c, b.namespace, stg.S3.CredentialsSecret)
			if err != nil {
				return errors.Wrap(err, "getting s3 credentials secret name")
			}

			conf.Storage.S3.Credentials = s3.Credentials{
				AccessKeyID:     string(s3secret.Data[awsAccessKeySecretKey]),
				SecretAccessKey: string(s3secret.Data[awsSecretAccessKeySecretKey]),
			}
		}
	case api.BackupStorageFilesystem:
		return errors.New("filesystem backup storage not supported yet, skipping storage name")
	case api.BackupStorageAzure:
		if stg.Azure.CredentialsSecret == "" {
			return errors.New("no credentials specified for the secret name")
		}
		azureSecret, err := secret(ctx, b.k8c, b.namespace, stg.Azure.CredentialsSecret)
		if err != nil {
			return errors.Wrap(err, "getting azure credentials secret name")
		}
		conf.Storage = pbm.StorageConf{
			Type: pbm.StorageAzure,
			Azure: azure.Conf{
				Account:   string(azureSecret.Data[azureStorageAccountNameSecretKey]),
				Container: stg.Azure.Container,
				Prefix:    stg.Azure.Prefix,
				Credentials: azure.Credentials{
					Key: string(azureSecret.Data[azureStorageAccountKeySecretKey]),
				},
			},
		}
	default:
		return errors.New("unsupported backup storage type")
	}

	if err := b.C.SetConfig(conf); err != nil {
		return errors.Wrap(err, "write config")
	}
	time.Sleep(11 * time.Second) // give time to init new storage

	return nil
}

// Close close the PBM connection
func (b *PBM) Close(ctx context.Context) error {
	return b.C.Conn.Disconnect(ctx)
}

func secret(ctx context.Context, cl client.Client, namespace, secretName string) (*corev1.Secret, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
	}
	err := cl.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, secret)
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
		rsTimelines, err := b.C.PITRGetValidTimelines(s.RS, primitive.Timestamp{T: uint32(now)}, nil)
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

// PITRGetChunkContains returns a pitr slice chunk that belongs to the
// given replica set and contains the given timestamp
func (p *PBM) pitrGetChunkContains(ctx context.Context, rs string, ts primitive.Timestamp) (*pbm.PITRChunk, error) {
	res := p.C.Conn.Database(pbm.DB).Collection(pbm.PITRChunksCollection).FindOne(
		ctx,
		bson.D{
			{"rs", rs},
			{"start_ts", bson.M{"$lte": ts}},
			{"end_ts", bson.M{"$gte": ts}},
		},
	)
	if res.Err() != nil {
		return nil, errors.Wrap(res.Err(), "get")
	}

	chnk := new(pbm.PITRChunk)
	err := res.Decode(chnk)
	return chnk, errors.Wrap(err, "decode")
}

func (b *PBM) GetPITRChunkContains(ctx context.Context, unixTS int64) (*pbm.PITRChunk, error) {
	nodeInfo, err := b.C.GetNodeInfo()
	if err != nil {
		return nil, errors.Wrap(err, "getting node information")
	}

	c, err := b.pitrGetChunkContains(ctx, nodeInfo.SetName, primitive.Timestamp{T: uint32(unixTS)})
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
