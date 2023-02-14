package backup

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"
	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	client "sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

const (
	agentContainerName               = "backup-agent"
	AWSAccessKeySecretKey            = "AWS_ACCESS_KEY_ID"
	AWSSecretAccessKeySecretKey      = "AWS_SECRET_ACCESS_KEY"
	AzureStorageAccountNameSecretKey = "AZURE_STORAGE_ACCOUNT_NAME"
	AzureStorageAccountKeySecretKey  = "AZURE_STORAGE_ACCOUNT_KEY"
)

type PBM struct {
	C         *pbm.PBM
	k8c       client.Client
	namespace string
}

func getMongoUri(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, addrs []string) (string, error) {
	usersSecretName := api.UserSecretName(cr)
	scr, err := getSecret(ctx, k8sclient, cr.Namespace, usersSecretName)
	if err != nil {
		return "", errors.Wrap(err, "get secrets")
	}

	murl := fmt.Sprintf("mongodb://%s:%s@%s/",
		url.QueryEscape(string(scr.Data["MONGODB_BACKUP_USER"])),
		url.QueryEscape(string(scr.Data["MONGODB_BACKUP_PASSWORD"])),
		strings.Join(addrs, ","),
	)

	if cr.Spec.UnsafeConf {
		return murl, nil
	}

	// PBM connection is opened from the operator pod. In order to use SSL
	// certificates of the cluster, we need to copy them to operator pod.
	// This is especially important if the user passes custom config to set
	// net.tls.mode to requireTLS.
	sslSecret, err := getSecret(ctx, k8sclient, cr.Namespace, cr.Spec.Secrets.SSL)
	if err != nil {
		return "", errors.Wrap(err, "get ssl secret")
	}

	tlsKey := sslSecret.Data["tls.key"]
	tlsCert := sslSecret.Data["tls.crt"]
	tlsPemFile := fmt.Sprintf("/tmp/%s-tls.pem", cr.Name)
	f, err := os.OpenFile(tlsPemFile, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return "", errors.Wrapf(err, "open %s", tlsPemFile)
	}
	defer f.Close()
	if _, err := f.Write(append(tlsKey, tlsCert...)); err != nil {
		return "", errors.Wrapf(err, "write TLS key and certificate to %s", tlsPemFile)
	}

	caCert := sslSecret.Data["ca.crt"]
	caCertFile := fmt.Sprintf("/tmp/%s-ca.crt", cr.Name)
	f, err = os.OpenFile(caCertFile, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return "", errors.Wrapf(err, "open %s", caCertFile)
	}
	defer f.Close()
	if _, err := f.Write(caCert); err != nil {
		return "", errors.Wrapf(err, "write CA certificate to %s", caCertFile)
	}

	murl += fmt.Sprintf(
		"?tls=true&tlsCertificateKeyFile=%s&tlsCAFile=%s&tlsAllowInvalidCertificates=true&tlsInsecure=true",
		tlsPemFile,
		caCertFile,
	)

	return murl, nil
}

// NewPBM creates a new connection to PBM.
// It should be closed after the last use with.
func NewPBM(ctx context.Context, c client.Client, cluster *api.PerconaServerMongoDB) (*PBM, error) {
	rs := cluster.Spec.Replsets[0]

	pods, err := psmdb.GetRSPods(ctx, c, cluster, rs.Name, false)
	if err != nil {
		return nil, errors.Wrapf(err, "get pods list for replset %s", rs.Name)
	}

	if len(cluster.Spec.ClusterServiceDNSSuffix) == 0 {
		cluster.Spec.ClusterServiceDNSSuffix = api.DefaultDNSSuffix
	}

	addrs, err := psmdb.GetReplsetAddrs(ctx, c, cluster, rs.Name, false, pods.Items)
	if err != nil {
		return nil, errors.Wrap(err, "get replset addrs")
	}

	murl, err := getMongoUri(ctx, c, cluster, addrs)
	if err != nil {
		return nil, errors.Wrap(err, "get mongo uri")
	}

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
func GetPriorities(ctx context.Context, k8sclient client.Client, cluster *api.PerconaServerMongoDB) (map[string]float64, error) {
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

func GetPBMConfig(ctx context.Context, k8sclient client.Client, cluster *api.PerconaServerMongoDB, stg api.BackupStorageSpec) (pbm.Config, error) {
	conf := pbm.Config{}

	priority, err := GetPriorities(ctx, k8sclient, cluster)
	if err != nil {
		return conf, errors.Wrap(err, "get priorities")
	}

	conf = pbm.Config{
		PITR: pbm.PITRConf{
			Enabled:          cluster.Spec.Backup.PITR.Enabled,
			Compression:      cluster.Spec.Backup.PITR.CompressionType,
			CompressionLevel: cluster.Spec.Backup.PITR.CompressionLevel,
		},
		Backup: pbm.BackupConf{
			Priority: priority,
		},
	}

	switch stg.Type {
	case api.BackupStorageS3:
		conf.Storage = pbm.StorageConf{
			Type: storage.S3,
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
			s3secret, err := getSecret(ctx, k8sclient, cluster.Namespace, stg.S3.CredentialsSecret)
			if err != nil {
				return conf, errors.Wrap(err, "get s3 credentials secret")
			}

			conf.Storage.S3.Credentials = s3.Credentials{
				AccessKeyID:     string(s3secret.Data[AWSAccessKeySecretKey]),
				SecretAccessKey: string(s3secret.Data[AWSSecretAccessKeySecretKey]),
			}
		}
	case api.BackupStorageAzure:
		if stg.Azure.CredentialsSecret == "" {
			return conf, errors.New("no credentials specified for the secret name")
		}
		azureSecret, err := getSecret(ctx, k8sclient, cluster.Namespace, stg.Azure.CredentialsSecret)
		if err != nil {
			return conf, errors.Wrap(err, "get azure credentials secret")
		}
		conf.Storage = pbm.StorageConf{
			Type: storage.Azure,
			Azure: azure.Conf{
				Account:   string(azureSecret.Data[AzureStorageAccountNameSecretKey]),
				Container: stg.Azure.Container,
				Prefix:    stg.Azure.Prefix,
				Credentials: azure.Credentials{
					Key: string(azureSecret.Data[AzureStorageAccountKeySecretKey]),
				},
			},
		}
	case api.BackupStorageFilesystem:
		return conf, errors.New("filesystem backup storage not supported yet, skipping storage name")
	default:
		return conf, errors.New("unsupported backup storage type")
	}

	return conf, nil
}

// SetConfig sets the pbm config with storage defined in the cluster CR
// by given storageName
func (b *PBM) SetConfig(ctx context.Context, k8sclient client.Client, cluster *api.PerconaServerMongoDB, stg api.BackupStorageSpec) error {
	conf, err := GetPBMConfig(ctx, k8sclient, cluster, stg)
	if err != nil {
		return errors.Wrap(err, "get PBM config")
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

func getSecret(ctx context.Context, cl client.Client, namespace, secretName string) (*corev1.Secret, error) {
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

func (b *PBM) GetLastPITRChunk() (*pbm.OplogChunk, error) {
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

	shards, err := b.C.ClusterMembers()
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
func (p *PBM) pitrGetChunkContains(ctx context.Context, rs string, ts primitive.Timestamp) (*pbm.OplogChunk, error) {
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

	chnk := new(pbm.OplogChunk)
	err := res.Decode(chnk)
	return chnk, errors.Wrap(err, "decode")
}

func (b *PBM) GetPITRChunkContains(ctx context.Context, unixTS int64) (*pbm.OplogChunk, error) {
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
