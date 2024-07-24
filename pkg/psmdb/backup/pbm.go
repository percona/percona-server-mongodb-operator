package backup

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	client "sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/lock"
	pbmLog "github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/oplog"
	"github.com/percona/percona-backup-mongodb/pbm/restore"
	"github.com/percona/percona-backup-mongodb/pbm/resync"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"
	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

const (
	agentContainerName               = "backup-agent"
	AWSAccessKeySecretKey            = "AWS_ACCESS_KEY_ID"
	AWSSecretAccessKeySecretKey      = "AWS_SECRET_ACCESS_KEY"
	AzureStorageAccountNameSecretKey = "AZURE_STORAGE_ACCOUNT_NAME"
	AzureStorageAccountKeySecretKey  = "AZURE_STORAGE_ACCOUNT_KEY"
	SSECustomerKey                   = "SSE_CUSTOMER_KEY"
	KMSKeyID                         = "KMS_KEY_ID"
)

type pbmC struct {
	connect.Client
	pbmLogger pbmLog.Logger
	k8c       client.Client
	namespace string
	rsName    string
}

type BackupMeta = backup.BackupMeta

type PBM interface {
	Conn() *mongo.Client

	GetPITRChunkContains(ctx context.Context, unixTS int64) (*oplog.OplogChunk, error)
	GetLatestTimelinePITR(ctx context.Context) (oplog.Timeline, error)
	PITRGetChunksSlice(ctx context.Context, rs string, from, to primitive.Timestamp) ([]oplog.OplogChunk, error)
	PITRChunksCollection() *mongo.Collection

	Logger() pbmLog.Logger
	GetStorage(ctx context.Context, e pbmLog.LogEvent) (storage.Storage, error)
	ResyncStorage(ctx context.Context, e pbmLog.LogEvent) error
	SendCmd(ctx context.Context, cmd ctrl.Cmd) error
	Close(ctx context.Context) error
	HasLocks(ctx context.Context, predicates ...LockHeaderPredicate) (bool, error)
	ValidateBackup(ctx context.Context, bcp *psmdbv1.PerconaServerMongoDBBackup, cfg config.Config) error

	GetBackupMeta(ctx context.Context, bcpName string) (*backup.BackupMeta, error)
	GetRestoreMeta(ctx context.Context, name string) (*restore.RestoreMeta, error)

	DeleteBackup(ctx context.Context, name string) error

	SetConfig(ctx context.Context, k8sclient client.Client, cluster *api.PerconaServerMongoDB, stg api.BackupStorageSpec) error
	SetConfigVar(ctx context.Context, key, val string) error
	GetConfigVar(ctx context.Context, key string) (any, error)
	DeleteConfigVar(ctx context.Context, key string) error

	Node(ctx context.Context) (string, error)
}

func getMongoUri(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, addrs []string, tlsEnabled bool) (string, error) {
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

	if !tlsEnabled {
		return murl, nil
	}

	// PBM connection is opened from the operator pod. In order to use SSL
	// certificates of the cluster, we need to copy them to operator pod.
	// This is especially important if the user passes custom config to set
	// net.tls.mode to requireTLS.
	sslSecret, err := getSecret(ctx, k8sclient, cr.Namespace, api.SSLSecretName(cr))
	if err != nil {
		return "", errors.Wrap(err, "get ssl secret")
	}

	tlsKey := sslSecret.Data["tls.key"]
	tlsCert := sslSecret.Data["tls.crt"]
	tlsPemFile := fmt.Sprintf("/tmp/%s-tls.pem", cr.Name)
	f, err := os.OpenFile(tlsPemFile, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return "", errors.Wrapf(err, "open %s", tlsPemFile)
	}
	defer f.Close()
	if _, err := f.Write(append(tlsKey, tlsCert...)); err != nil {
		return "", errors.Wrapf(err, "write TLS key and certificate to %s", tlsPemFile)
	}

	caCert := sslSecret.Data["ca.crt"]
	caCertFile := fmt.Sprintf("/tmp/%s-ca.crt", cr.Name)
	f, err = os.OpenFile(caCertFile, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0o600)
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

type NewPBMFunc func(ctx context.Context, c client.Client, cluster *api.PerconaServerMongoDB) (PBM, error)

// NewPBM creates a new connection to PBM.
// It should be closed after the last use with.
func NewPBM(ctx context.Context, c client.Client, cluster *api.PerconaServerMongoDB) (PBM, error) {
	rs := cluster.Spec.Replsets[0]

	pods, err := psmdb.GetRSPods(ctx, c, cluster, rs.Name)
	if err != nil {
		return nil, errors.Wrapf(err, "get pods list for replset %s", rs.Name)
	}

	if len(cluster.Spec.ClusterServiceDNSSuffix) == 0 {
		cluster.Spec.ClusterServiceDNSSuffix = api.DefaultDNSSuffix
	}

	addrs, err := psmdb.GetReplsetAddrs(ctx, c, cluster, cluster.Spec.ClusterServiceDNSMode, rs.Name, false, pods.Items)
	if err != nil {
		return nil, errors.Wrap(err, "get replset addrs")
	}

	murl, err := getMongoUri(ctx, c, cluster, addrs, cluster.TLSEnabled())
	if err != nil {
		return nil, errors.Wrap(err, "get mongo uri")
	}

	pbmc, err := connect.Connect(ctx, murl, "operator-pbm-ctl")
	if err != nil {
		return nil, errors.Wrapf(err, "create PBM connection to %s", strings.Join(addrs, ","))
	}

	return &pbmC{
		Client:    pbmc,
		pbmLogger: pbmLog.New(pbmc.LogCollection(), "", ""),
		k8c:       c,
		namespace: cluster.Namespace,
		rsName:    rs.Name,
	}, nil
}

// GetPriorities returns priorities to be used in PBM config.
func GetPriorities(ctx context.Context, k8sclient client.Client, cluster *api.PerconaServerMongoDB) (map[string]float64, error) {
	log := logf.FromContext(ctx)
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

		if disconnectErr := cli.Disconnect(ctx); disconnectErr != nil {
			log.Error(err, "failed to close connection to replicaSet", "rs", rs.Name)
		}

		if err != nil {
			return priorities, errors.Wrap(err, "get primary pod")
		}
		priorities[primary] = 0.5
	}

	return priorities, nil
}

func GetPBMConfig(ctx context.Context, k8sclient client.Client, cluster *api.PerconaServerMongoDB, stg api.BackupStorageSpec) (config.Config, error) {
	conf := config.Config{
		PITR: config.PITRConf{
			Enabled:          cluster.Spec.Backup.PITR.Enabled,
			Compression:      cluster.Spec.Backup.PITR.CompressionType,
			CompressionLevel: cluster.Spec.Backup.PITR.CompressionLevel,
		},
	}

	if cluster.Spec.Backup.Configuration.BackupOptions != nil {
		conf.Backup = config.BackupConf{
			OplogSpanMin: cluster.Spec.Backup.Configuration.BackupOptions.OplogSpanMin,
			Timeouts: &config.BackupTimeouts{
				Starting: cluster.Spec.Backup.Configuration.BackupOptions.Timeouts.Starting,
			},
		}

		if cluster.Spec.Backup.Configuration.BackupOptions.Priority != nil {
			conf.Backup.Priority = cluster.Spec.Backup.Configuration.BackupOptions.Priority
		} else {
			priority, err := GetPriorities(ctx, k8sclient, cluster)
			if err != nil {
				return conf, errors.Wrap(err, "get priorities")
			}
			conf.Backup.Priority = priority
		}
	}

	if cluster.Spec.Backup.Configuration.RestoreOptions != nil {
		conf.Restore = config.RestoreConf{
			BatchSize:           cluster.Spec.Backup.Configuration.RestoreOptions.BatchSize,
			NumInsertionWorkers: cluster.Spec.Backup.Configuration.RestoreOptions.NumInsertionWorkers,
			NumDownloadWorkers:  cluster.Spec.Backup.Configuration.RestoreOptions.NumDownloadWorkers,
			MaxDownloadBufferMb: cluster.Spec.Backup.Configuration.RestoreOptions.MaxDownloadBufferMb,
			DownloadChunkMb:     cluster.Spec.Backup.Configuration.RestoreOptions.DownloadChunkMb,
			MongodLocation:      cluster.Spec.Backup.Configuration.RestoreOptions.MongodLocation,
			MongodLocationMap:   cluster.Spec.Backup.Configuration.RestoreOptions.MongodLocationMap,
		}
	}

	switch stg.Type {
	case api.BackupStorageS3:
		conf.Storage = config.StorageConf{
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

			if len(stg.S3.ServerSideEncryption.SSECustomerAlgorithm) != 0 {
				switch {
				case len(stg.S3.ServerSideEncryption.SSECustomerKey) != 0:
					conf.Storage.S3.ServerSideEncryption = &s3.AWSsse{
						SseCustomerAlgorithm: stg.S3.ServerSideEncryption.SSECustomerAlgorithm,
						SseCustomerKey:       stg.S3.ServerSideEncryption.SSECustomerKey,
					}
				case len(cluster.Spec.Secrets.SSE) != 0:
					sseSecret, err := getSecret(ctx, k8sclient, cluster.Namespace, cluster.Spec.Secrets.SSE)
					if err != nil {
						return conf, errors.Wrap(err, "get sse credentials secret")
					}
					conf.Storage.S3.ServerSideEncryption = &s3.AWSsse{
						SseCustomerAlgorithm: stg.S3.ServerSideEncryption.SSECustomerAlgorithm,
						SseCustomerKey:       string(sseSecret.Data[SSECustomerKey]),
					}
				default:
					return conf, errors.New("no SseCustomerKey specified")
				}
			}

			if len(stg.S3.ServerSideEncryption.SSEAlgorithm) != 0 {
				switch {
				case len(stg.S3.ServerSideEncryption.KMSKeyID) != 0:
					conf.Storage.S3.ServerSideEncryption = &s3.AWSsse{
						SseAlgorithm: stg.S3.ServerSideEncryption.SSEAlgorithm,
						KmsKeyID:     stg.S3.ServerSideEncryption.KMSKeyID,
					}

				case len(cluster.Spec.Secrets.SSE) != 0:
					sseSecret, err := getSecret(ctx, k8sclient, cluster.Namespace, cluster.Spec.Secrets.SSE)
					if err != nil {
						return conf, errors.Wrap(err, "get sse credentials secret")
					}
					conf.Storage.S3.ServerSideEncryption = &s3.AWSsse{
						SseAlgorithm: stg.S3.ServerSideEncryption.SSEAlgorithm,
						KmsKeyID:     string(sseSecret.Data[KMSKeyID]),
					}
				default:
					return conf, errors.New("no KmsKeyID specified")
				}
			}
			conf.Storage.S3.Credentials = s3.Credentials{
				AccessKeyID:     string(s3secret.Data[AWSAccessKeySecretKey]),
				SecretAccessKey: string(s3secret.Data[AWSSecretAccessKeySecretKey]),
			}
		}

		if stg.S3.Retryer != nil {
			conf.Storage.S3.Retryer = &s3.Retryer{
				NumMaxRetries: stg.S3.Retryer.NumMaxRetries,
				MinRetryDelay: stg.S3.Retryer.MinRetryDelay.Duration,
				MaxRetryDelay: stg.S3.Retryer.MaxRetryDelay.Duration,
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
		conf.Storage = config.StorageConf{
			Type: storage.Azure,
			Azure: azure.Conf{
				Account:     string(azureSecret.Data[AzureStorageAccountNameSecretKey]),
				Container:   stg.Azure.Container,
				EndpointURL: stg.Azure.EndpointURL,
				Prefix:      stg.Azure.Prefix,
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

func (b *pbmC) ValidateBackup(ctx context.Context, bcp *psmdbv1.PerconaServerMongoDBBackup, cfg config.Config) error {
	e := b.Logger().NewEvent(string(ctrl.CmdRestore), "", "", primitive.Timestamp{})
	backupName := bcp.Status.PBMname
	s, err := util.StorageFromConfig(cfg.Storage, e)
	if err != nil {
		return errors.Wrap(err, "storage from config")
	}
	m, err := restore.GetMetaFromStore(s, backupName)
	if err != nil {
		return errors.Wrap(err, "get backup metadata from storage")
	}
	switch bcp.Status.Type {
	case "", defs.LogicalBackup:
		if err := backup.CheckBackupFiles(ctx, m, s); err != nil {
			return errors.Wrap(err, "check backup files")
		}
	case defs.PhysicalBackup:
		for _, rs := range m.Replsets {
			f := path.Join(m.Name, rs.Name)
			files, err := s.List(f, "")
			if err != nil {
				return errors.Wrapf(err, "failed to list backup files at %s", f)
			}
			fmt.Println("TEST: ", files)
			if len(files) == 0 {
				return errors.Wrap(err, "no physical backup files")
			}
		}
	}

	return nil
}

func (b *pbmC) Conn() *mongo.Client {
	return b.Client.MongoClient()
}

// SetConfig sets the pbm config with storage defined in the cluster CR
// by given storageName
func (b *pbmC) SetConfig(ctx context.Context, k8sclient client.Client, cluster *api.PerconaServerMongoDB, stg api.BackupStorageSpec) error {
	log := logf.FromContext(ctx)
	log.Info("Setting PBM config", "backup", cluster.Name)

	conf, err := GetPBMConfig(ctx, k8sclient, cluster, stg)
	if err != nil {
		return errors.Wrap(err, "get PBM config")
	}

	if err := config.SetConfig(ctx, b.Client, &conf); err != nil {
		return errors.Wrap(err, "write config")
	}

	time.Sleep(11 * time.Second) // give time to init new storage
	return nil
}

// Close close the PBM connection
func (b *pbmC) Close(ctx context.Context) error {
	return b.Client.Disconnect(ctx)
}

func (b *pbmC) Logger() pbmLog.Logger {
	return b.pbmLogger
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

type LockHeaderPredicate func(lock.LockHeader) bool

func NotPITRLock(l lock.LockHeader) bool {
	return l.Type != ctrl.CmdPITR
}

func IsPITRLock(l lock.LockHeader) bool {
	return l.Type == ctrl.CmdPITR
}

func NotJobLock(j Job) LockHeaderPredicate {
	return func(h lock.LockHeader) bool {
		var jobCommand ctrl.Command

		switch j.Type {
		case TypeBackup:
			jobCommand = ctrl.CmdBackup
		case TypeRestore:
			jobCommand = ctrl.CmdRestore
		case TypePITRestore:
			jobCommand = ctrl.CmdRestore
		default:
			return true
		}

		return h.Type != jobCommand
	}
}

func (b *pbmC) HasLocks(ctx context.Context, predicates ...LockHeaderPredicate) (bool, error) {
	locks, err := lock.GetLocks(ctx, b.Client, &lock.LockHeader{})
	if err != nil {
		return false, errors.Wrap(err, "get lock data")
	}

	opLocks, err := lock.GetOpLocks(ctx, b.Client, &lock.LockHeader{})
	if err != nil {
		return false, errors.Wrap(err, "get op lock data")
	}

	locks = append(locks, opLocks...)

	allowedByAllPredicates := func(l lock.LockHeader) bool {
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

var ErrNoOplogsForPITR = errors.New("there is no oplogs that can cover the date/time or no oplogs at all")

func (b *pbmC) GetLastPITRChunk(ctx context.Context) (*oplog.OplogChunk, error) {
	nodeInfo, err := topo.GetNodeInfo(context.TODO(), b.Client.MongoClient())
	if err != nil {
		return nil, errors.Wrap(err, "getting node information")
	}

	c, err := oplog.PITRLastChunkMeta(ctx, b.Client, nodeInfo.SetName)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrNoOplogsForPITR
		}
		return nil, errors.Wrap(err, "getting last PITR chunk")
	}

	if c == nil {
		return nil, ErrNoOplogsForPITR
	}

	return c, nil
}

func (b *pbmC) GetTimelinesPITR(ctx context.Context) ([]oplog.Timeline, error) {
	var (
		now       = time.Now().UTC().Unix()
		timelines [][]oplog.Timeline
	)

	shards, err := topo.ClusterMembers(ctx, b.Client.MongoClient())
	if err != nil {
		return nil, errors.Wrap(err, "getting cluster members")
	}

	for _, s := range shards {
		rsTimelines, err := oplog.PITRGetValidTimelines(ctx, b.Client, s.RS, primitive.Timestamp{T: uint32(now)})
		if err != nil {
			return nil, errors.Wrapf(err, "getting timelines for %s", s.RS)
		}

		timelines = append(timelines, rsTimelines)
	}

	return oplog.MergeTimelines(timelines...), nil
}

func (b *pbmC) GetLatestTimelinePITR(ctx context.Context) (oplog.Timeline, error) {
	timelines, err := b.GetTimelinesPITR(ctx)
	if err != nil {
		return oplog.Timeline{}, err
	}

	if len(timelines) == 0 {
		return oplog.Timeline{}, ErrNoOplogsForPITR
	}

	tl := timelines[len(timelines)-1]
	if tl.Start == 0 || tl.End == 0 {
		return oplog.Timeline{}, ErrNoOplogsForPITR
	}

	return tl, nil
}

// PITRGetChunkContains returns a pitr slice chunk that belongs to the
// given replica set and contains the given timestamp
func (b *pbmC) pitrGetChunkContains(ctx context.Context, rs string, ts primitive.Timestamp) (*oplog.OplogChunk, error) {
	res := b.Client.PITRChunksCollection().FindOne(
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

	chnk := new(oplog.OplogChunk)
	err := res.Decode(chnk)
	return chnk, errors.Wrap(err, "decode")
}

func (b *pbmC) GetPITRChunkContains(ctx context.Context, unixTS int64) (*oplog.OplogChunk, error) {
	nodeInfo, err := topo.GetNodeInfo(ctx, b.Client.MongoClient())
	if err != nil {
		return nil, errors.Wrap(err, "getting node information")
	}

	c, err := b.pitrGetChunkContains(ctx, nodeInfo.SetName, primitive.Timestamp{T: uint32(unixTS)})
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrNoOplogsForPITR
		}
		return nil, errors.Wrap(err, "getting PITR chunk for ts")
	}

	if c == nil {
		return nil, ErrNoOplogsForPITR
	}

	return c, nil
}

func (b *pbmC) PITRGetChunksSlice(ctx context.Context, rsName string, from, to primitive.Timestamp) ([]oplog.OplogChunk, error) {
	return oplog.PITRGetChunksSlice(ctx, b.Client, rsName, from, to)
}

// Node returns replset node chosen to run the backup for a replset related to pbmC
func (b *pbmC) Node(ctx context.Context) (string, error) {
	lock, err := lock.GetLockData(ctx, b.Client, &lock.LockHeader{Replset: b.rsName})
	if err != nil {
		return "", err
	}

	return strings.Split(lock.Node, ".")[0], nil
}

func (b *pbmC) GetStorage(ctx context.Context, e pbmLog.LogEvent) (storage.Storage, error) {
	return util.GetStorage(ctx, b.Client, e)
}

func (b *pbmC) GetConfigVar(ctx context.Context, key string) (any, error) {
	return config.GetConfigVar(ctx, b.Client, key)
}

func (b *pbmC) SetConfigVar(ctx context.Context, key, val string) error {
	return config.SetConfigVar(ctx, b.Client, key, val)
}

func (b *pbmC) DeleteConfigVar(ctx context.Context, key string) error {
	return config.DeleteConfigVar(ctx, b.Client, key)
}

func (b *pbmC) GetBackupMeta(ctx context.Context, bcpName string) (*backup.BackupMeta, error) {
	return backup.NewDBManager(b.Client).GetBackupByName(ctx, bcpName)
}

func (b *pbmC) DeleteBackup(ctx context.Context, name string) error {
	return backup.DeleteBackup(ctx, b.Client, name)
}

func (b *pbmC) GetRestoreMeta(ctx context.Context, name string) (*restore.RestoreMeta, error) {
	return restore.GetRestoreMeta(ctx, b.Client, name)
}

func (b *pbmC) ResyncStorage(ctx context.Context, e pbmLog.LogEvent) error {
	return resync.ResyncStorage(ctx, b.Client, e)
}

func (b *pbmC) SendCmd(ctx context.Context, cmd ctrl.Cmd) error {
	cmd.TS = time.Now().UTC().Unix()
	_, err := b.CmdStreamCollection().InsertOne(ctx, cmd)
	return err
}

func (b *pbmC) PITRChunksCollection() *mongo.Collection {
	return b.Client.PITRChunksCollection()
}
