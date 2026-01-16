package backup

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
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
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"
	"github.com/percona/percona-backup-mongodb/pbm/storage/fs"
	"github.com/percona/percona-backup-mongodb/pbm/storage/gcs"
	"github.com/percona/percona-backup-mongodb/pbm/storage/mio"
	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

const (
	KMSKeyID                         = "KMS_KEY_ID"
	SSECustomerKey                   = "SSE_CUSTOMER_KEY"
	AWSAccessKeySecretKey            = "AWS_ACCESS_KEY_ID"
	AWSSecretAccessKeySecretKey      = "AWS_SECRET_ACCESS_KEY"
	AzureStorageAccountNameSecretKey = "AZURE_STORAGE_ACCOUNT_NAME"
	AzureStorageAccountKeySecretKey  = "AZURE_STORAGE_ACCOUNT_KEY"
	GCSClientEmailSecretKey          = "GCS_CLIENT_EMAIL"
	GCSPrivateKeySecretKey           = "GCS_PRIVATE_KEY"
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
	AgentStatuses(ctx context.Context, hosts []string) ([]topo.AgentStat, error)

	GetPITRChunkContains(ctx context.Context, unixTS int64, rsMap map[string]string) (*oplog.OplogChunk, error)
	GetLatestTimelinePITR(ctx context.Context, rsMap map[string]string) (oplog.Timeline, error)
	PITRGetChunksSlice(ctx context.Context, rs string, from, to primitive.Timestamp) ([]oplog.OplogChunk, error)
	PITRChunksCollection() *mongo.Collection

	Logger() pbmLog.Logger
	GetStorage(ctx context.Context, e pbmLog.LogEvent) (storage.Storage, error)
	SendCmd(ctx context.Context, cmd ctrl.Cmd) error
	Close(ctx context.Context) error
	HasLocks(ctx context.Context, predicates ...LockHeaderPredicate) (bool, error)
	ValidateBackup(ctx context.Context, cfg *config.Config, bcp *psmdbv1.PerconaServerMongoDBBackup) error

	ResyncMainStorage(ctx context.Context) error
	ResyncMainStorageAndWait(ctx context.Context) error
	ResyncProfile(ctx context.Context, name string) error
	ResyncProfileAndWait(ctx context.Context, name string) error

	GetBackupMeta(ctx context.Context, bcpName string) (*backup.BackupMeta, error)
	GetRestoreMeta(ctx context.Context, name string) (*restore.RestoreMeta, error)

	DeleteBackup(ctx context.Context, name string) error

	AddProfile(ctx context.Context, k8sclient client.Client, cluster *psmdbv1.PerconaServerMongoDB, name string, stg psmdbv1.BackupStorageSpec) error
	GetProfile(ctx context.Context, name string) (*config.Config, error)
	RemoveProfile(ctx context.Context, name string) error
	GetNSetConfig(ctx context.Context, k8sclient client.Client, cluster *psmdbv1.PerconaServerMongoDB) error
	GetNSetConfigLegacy(ctx context.Context, k8sclient client.Client, cluster *psmdbv1.PerconaServerMongoDB, stg psmdbv1.BackupStorageSpec) error
	SetConfig(ctx context.Context, cfg *config.Config) error
	SetConfigVar(ctx context.Context, key, val string) error

	GetConfig(ctx context.Context) (*config.Config, error)
	GetConfigVar(ctx context.Context, key string) (any, error)

	DeletePITRChunks(ctx context.Context, until primitive.Timestamp) error

	Node(ctx context.Context) (string, error)
}

func IsErrNoDocuments(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, mongo.ErrNoDocuments) || strings.Contains(err.Error(), "no documents in result")
}

func getMongoUri(ctx context.Context, k8sclient client.Client, cr *psmdbv1.PerconaServerMongoDB, addrs []string, tlsEnabled bool) (string, error) {
	usersSecretName := psmdbv1.UserSecretName(cr)
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
	sslSecret, err := getSecret(ctx, k8sclient, cr.Namespace, psmdbv1.SSLSecretName(cr))
	if err != nil {
		return "", errors.Wrap(err, "get ssl secret")
	}

	isCertFileOutdated := func(certData []byte, certFilePath string) (bool, error) {
		_, err := os.Stat(certFilePath)
		if os.IsNotExist(err) {
			return true, nil
		}

		fileData, err := os.ReadFile(certFilePath)
		if err != nil {
			return true, err
		}

		return !bytes.Equal(fileData, certData), nil
	}

	writeCertFileIfOutdated := func(certData []byte, filePath string) error {
		if isCertOutdated, err := isCertFileOutdated(certData, filePath); err != nil {
			return err
		} else if isCertOutdated {
			return os.WriteFile(filePath, certData, 0o600)
		}
		return nil
	}

	tlsKey := sslSecret.Data["tls.key"]
	tlsCert := sslSecret.Data["tls.crt"]
	tlsPemFile := fmt.Sprintf("/tmp/%s-%s-tls.pem", cr.Namespace, cr.Name)
	tlsPem := append(tlsKey, tlsCert...)

	err = writeCertFileIfOutdated(tlsPem, tlsPemFile)
	if err != nil {
		return "", errors.Wrapf(err, "error checking and writing TLS key and certificate to file %s", tlsPemFile)
	}

	caCert := sslSecret.Data["ca.crt"]
	caCertFile := fmt.Sprintf("/tmp/%s-%s-ca.crt", cr.Namespace, cr.Name)

	err = writeCertFileIfOutdated(caCert, caCertFile)
	if err != nil {
		return "", errors.Wrapf(err, "error checking and writing CA certificate to file %s", tlsPemFile)
	}

	murl += fmt.Sprintf(
		"?tls=true&tlsCertificateKeyFile=%s&tlsCAFile=%s&tlsAllowInvalidCertificates=true&tlsInsecure=true",
		tlsPemFile,
		caCertFile,
	)

	return murl, nil
}

type NewPBMFunc func(ctx context.Context, c client.Client, cluster *psmdbv1.PerconaServerMongoDB) (PBM, error)

// NewPBM creates a new connection to PBM.
// It should be closed after the last use with.
func NewPBM(ctx context.Context, c client.Client, cluster *psmdbv1.PerconaServerMongoDB) (PBM, error) {
	rs := cluster.Spec.Replsets[0]

	pods, err := psmdb.GetRSPods(ctx, c, cluster, rs.Name)
	if err != nil {
		return nil, errors.Wrapf(err, "get pods list for replset %s", rs.Name)
	}

	if len(cluster.Spec.ClusterServiceDNSSuffix) == 0 {
		cluster.Spec.ClusterServiceDNSSuffix = psmdbv1.DefaultDNSSuffix
	}

	addrs, err := psmdb.GetReplsetAddrs(ctx, c, cluster, cluster.Spec.ClusterServiceDNSMode, rs, false, pods.Items)
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
		pbmLogger: pbmLog.New(pbmc, "", ""),
		k8c:       c,
		namespace: cluster.Namespace,
		rsName:    rs.Name,
	}, nil
}

// GetPriorities returns priorities to be used in PBM config.
func GetPriorities(ctx context.Context, k8sclient client.Client, cluster *psmdbv1.PerconaServerMongoDB) (map[string]float64, error) {
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

		cli, err := psmdb.MongoClient(ctx, k8sclient, cluster, rs, c)
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

// GetPBMConfig returns PBM configuration with given storage.
func GetPBMConfig(ctx context.Context, k8sclient client.Client, cluster *psmdbv1.PerconaServerMongoDB, stg psmdbv1.BackupStorageSpec) (config.Config, error) {
	conf := config.Config{
		PITR: &config.PITRConf{
			Enabled:          cluster.Spec.Backup.PITR.Enabled,
			Compression:      cluster.Spec.Backup.PITR.CompressionType,
			CompressionLevel: cluster.Spec.Backup.PITR.CompressionLevel,
		},
	}

	if opts := cluster.Spec.Backup.Configuration.BackupOptions; opts != nil {
		conf.Backup = &config.BackupConf{
			OplogSpanMin:           opts.OplogSpanMin,
			NumParallelCollections: opts.NumParallelCollections,
		}

		if opts.Timeouts != nil {
			conf.Backup.Timeouts = &config.BackupTimeouts{
				Starting: opts.Timeouts.Starting,
			}
		}

		if opts.Priority != nil {
			conf.Backup.Priority = opts.Priority
		} else {
			priority, err := GetPriorities(ctx, k8sclient, cluster)
			if err != nil {
				return conf, errors.Wrap(err, "get priorities")
			}
			conf.Backup.Priority = priority
		}
	}

	if cluster.Spec.Backup.Configuration.RestoreOptions != nil {
		conf.Restore = &config.RestoreConf{
			BatchSize:              cluster.Spec.Backup.Configuration.RestoreOptions.BatchSize,
			NumInsertionWorkers:    cluster.Spec.Backup.Configuration.RestoreOptions.NumInsertionWorkers,
			NumDownloadWorkers:     cluster.Spec.Backup.Configuration.RestoreOptions.NumDownloadWorkers,
			NumParallelCollections: cluster.Spec.Backup.Configuration.RestoreOptions.NumParallelCollections,
			MaxDownloadBufferMb:    cluster.Spec.Backup.Configuration.RestoreOptions.MaxDownloadBufferMb,
			DownloadChunkMb:        cluster.Spec.Backup.Configuration.RestoreOptions.DownloadChunkMb,
			MongodLocation:         cluster.Spec.Backup.Configuration.RestoreOptions.MongodLocation,
			MongodLocationMap:      cluster.Spec.Backup.Configuration.RestoreOptions.MongodLocationMap,
		}
	}

	storageConf, err := GetPBMStorageConfig(ctx, k8sclient, cluster, stg)
	if err != nil {
		return conf, errors.Wrap(err, "get storage config")
	}
	conf.Storage = storageConf

	return conf, nil
}

func GetPBMStorageMinioConfig(
	ctx context.Context,
	k8sclient client.Client,
	cluster *psmdbv1.PerconaServerMongoDB,
	stg psmdbv1.BackupStorageSpec,
) (config.StorageConf, error) {
	if err := stg.Minio.Validate(); err != nil {
		return config.StorageConf{}, errors.Wrap(err, "invalid minio storage config")
	}

	storageConf := config.StorageConf{
		Type: storage.Minio,
		Minio: &mio.Config{
			Region:                stg.Minio.Region,
			Endpoint:              stg.Minio.EndpointURL,
			Bucket:                stg.Minio.Bucket,
			Prefix:                stg.Minio.Prefix,
			InsecureSkipTLSVerify: stg.Minio.InsecureSkipTLSVerify,
			DebugTrace:            stg.Minio.DebugTrace,
			PartSize:              stg.Minio.PartSize,
			Secure:                stg.Minio.Secure,
		},
	}

	if len(stg.Minio.CredentialsSecret) != 0 {
		s3secret, err := getSecret(ctx, k8sclient, cluster.GetNamespace(), stg.Minio.CredentialsSecret)
		if err != nil {
			return config.StorageConf{}, errors.Wrap(err, "get minio credentials secret")
		}

		accessKey, ok := s3secret.Data[AWSAccessKeySecretKey]
		if !ok {
			return config.StorageConf{}, errors.New("access key not found in credentials secret")
		}
		secretAccessKey, ok := s3secret.Data[AWSSecretAccessKeySecretKey]
		if !ok {
			return config.StorageConf{}, errors.New("secret access key not found in credentials secret")
		}

		storageConf.Minio.Credentials = mio.Credentials{
			AccessKeyID:     string(accessKey),
			SecretAccessKey: string(secretAccessKey),
		}
	}

	if stg.Minio.Retryer != nil {
		storageConf.Minio.Retryer = &mio.Retryer{
			NumMaxRetries: stg.Minio.Retryer.NumMaxRetries,
		}
	}
	return storageConf, nil
}

func GetPBMStorageS3Config(
	ctx context.Context,
	k8sclient client.Client,
	cluster *psmdbv1.PerconaServerMongoDB,
	stg psmdbv1.BackupStorageSpec,
) (config.StorageConf, error) {
	storageConf := config.StorageConf{
		Type: storage.S3,
		S3: &s3.Config{
			Region:                stg.S3.Region,
			EndpointURL:           stg.S3.EndpointURL,
			Bucket:                stg.S3.Bucket,
			Prefix:                stg.S3.Prefix,
			UploadPartSize:        stg.S3.UploadPartSize,
			MaxUploadParts:        stg.S3.MaxUploadParts,
			StorageClass:          stg.S3.StorageClass,
			InsecureSkipTLSVerify: stg.S3.InsecureSkipTLSVerify,
			ForcePathStyle:        stg.S3.ForcePathStyle,
		},
	}

	if len(stg.S3.CredentialsSecret) != 0 {
		s3secret, err := getSecret(ctx, k8sclient, cluster.Namespace, stg.S3.CredentialsSecret)
		if err != nil {
			return storageConf, errors.Wrap(err, "get s3 credentials secret")
		}

		if len(stg.S3.ServerSideEncryption.SSECustomerAlgorithm) != 0 {
			switch {
			case len(stg.S3.ServerSideEncryption.SSECustomerKey) != 0:
				storageConf.S3.ServerSideEncryption = &s3.AWSsse{
					SseCustomerAlgorithm: stg.S3.ServerSideEncryption.SSECustomerAlgorithm,
					SseCustomerKey:       stg.S3.ServerSideEncryption.SSECustomerKey,
				}
			case len(cluster.Spec.Secrets.SSE) != 0:
				sseSecret, err := getSecret(ctx, k8sclient, cluster.Namespace, cluster.Spec.Secrets.SSE)
				if err != nil {
					return storageConf, errors.Wrap(err, "get sse credentials secret")
				}
				storageConf.S3.ServerSideEncryption = &s3.AWSsse{
					SseCustomerAlgorithm: stg.S3.ServerSideEncryption.SSECustomerAlgorithm,
					SseCustomerKey:       string(sseSecret.Data[SSECustomerKey]),
				}
			default:
				return storageConf, errors.New("no SseCustomerKey specified")
			}
		}

		if len(stg.S3.ServerSideEncryption.SSEAlgorithm) != 0 {
			switch {
			case len(stg.S3.ServerSideEncryption.KMSKeyID) != 0:
				storageConf.S3.ServerSideEncryption = &s3.AWSsse{
					SseAlgorithm: stg.S3.ServerSideEncryption.SSEAlgorithm,
					KmsKeyID:     stg.S3.ServerSideEncryption.KMSKeyID,
				}

			case len(cluster.Spec.Secrets.SSE) != 0:
				sseSecret, err := getSecret(ctx, k8sclient, cluster.Namespace, cluster.Spec.Secrets.SSE)
				if err != nil {
					return storageConf, errors.Wrap(err, "get sse credentials secret")
				}
				storageConf.S3.ServerSideEncryption = &s3.AWSsse{
					SseAlgorithm: stg.S3.ServerSideEncryption.SSEAlgorithm,
					KmsKeyID:     string(sseSecret.Data[KMSKeyID]),
				}
			default:
				return storageConf, errors.New("no KmsKeyID specified")
			}
		}
		storageConf.S3.Credentials = s3.Credentials{
			AccessKeyID:     string(s3secret.Data[AWSAccessKeySecretKey]),
			SecretAccessKey: string(s3secret.Data[AWSSecretAccessKeySecretKey]),
		}
	}

	if stg.S3.Retryer != nil {
		storageConf.S3.Retryer = &s3.Retryer{
			NumMaxRetries: stg.S3.Retryer.NumMaxRetries,
			MinRetryDelay: stg.S3.Retryer.MinRetryDelay.Duration,
			MaxRetryDelay: stg.S3.Retryer.MaxRetryDelay.Duration,
		}
	}

	return storageConf, nil
}

func getGCSFromS3CompatibleConfig(cfg *s3.Config) *gcs.Config {
	return &gcs.Config{
		Bucket:    cfg.Bucket,
		Prefix:    cfg.Prefix,
		ChunkSize: cfg.UploadPartSize,
		Credentials: gcs.Credentials{
			HMACAccessKey: cfg.Credentials.AccessKeyID,
			HMACSecret:    cfg.Credentials.SecretAccessKey,
		},
	}
}

func GetPBMStorageGCSConfig(
	ctx context.Context,
	k8sclient client.Client,
	cluster *psmdbv1.PerconaServerMongoDB,
	stg psmdbv1.BackupStorageSpec,
) (config.StorageConf, error) {
	storageConf := config.StorageConf{
		Type: storage.GCS,
		GCS: &gcs.Config{
			Bucket:    stg.GCS.Bucket,
			Prefix:    stg.GCS.Prefix,
			ChunkSize: stg.GCS.ChunkSize,
		},
	}

	if stg.GCS.CredentialsSecret != "" {
		gcsSecret, err := getSecret(ctx, k8sclient, cluster.Namespace, stg.GCS.CredentialsSecret)
		if err != nil {
			return config.StorageConf{}, errors.Wrap(err, "get GCS credentials secret")
		}

		if _, ok := gcsSecret.Data[GCSClientEmailSecretKey]; ok {
			storageConf.GCS.Credentials = gcs.Credentials{
				ClientEmail: string(gcsSecret.Data[GCSClientEmailSecretKey]),
				PrivateKey:  string(gcsSecret.Data[GCSPrivateKeySecretKey]),
			}
		}

		// s3 compatibility
		if _, ok := gcsSecret.Data[AWSAccessKeySecretKey]; ok {
			storageConf.GCS.Credentials = gcs.Credentials{
				HMACAccessKey: string(gcsSecret.Data[AWSAccessKeySecretKey]),
				HMACSecret:    string(gcsSecret.Data[AWSSecretAccessKeySecretKey]),
			}
		}
	}

	if stg.GCS.Retryer != nil {
		storageConf.GCS.Retryer = &gcs.Retryer{
			BackoffInitial:    stg.GCS.Retryer.BackoffInitial,
			BackoffMax:        stg.GCS.Retryer.BackoffMax,
			BackoffMultiplier: stg.GCS.Retryer.BackoffMultiplier,
		}
	}

	return storageConf, nil
}

func GetPBMStorageS3CompatibleGCSConfig(
	ctx context.Context,
	k8sclient client.Client,
	cluster *psmdbv1.PerconaServerMongoDB,
	stg psmdbv1.BackupStorageSpec,
) (config.StorageConf, error) {
	gcs := psmdbv1.BackupStorageSpec{
		Type: psmdbv1.BackupStorageGCS,
		GCS: psmdbv1.BackupStorageGCSSpec{
			Bucket:            stg.S3.Bucket,
			Prefix:            stg.S3.Prefix,
			ChunkSize:         stg.S3.UploadPartSize,
			CredentialsSecret: stg.S3.CredentialsSecret,
		},
	}

	conf, err := GetPBMStorageGCSConfig(ctx, k8sclient, cluster, gcs)
	if err != nil {
		return config.StorageConf{}, errors.Wrap(err, "get gcs config")
	}

	return conf, nil
}

func GetPBMStorageAzureConfig(
	ctx context.Context,
	k8sclient client.Client,
	cluster *psmdbv1.PerconaServerMongoDB,
	stg psmdbv1.BackupStorageSpec,
) (config.StorageConf, error) {
	if stg.Azure.CredentialsSecret == "" {
		return config.StorageConf{}, errors.New("no credentials specified for the secret name")
	}

	azureSecret, err := getSecret(ctx, k8sclient, cluster.Namespace, stg.Azure.CredentialsSecret)
	if err != nil {
		return config.StorageConf{}, errors.Wrap(err, "get azure credentials secret")
	}

	storageConf := config.StorageConf{
		Type: storage.Azure,
		Azure: &azure.Config{
			Account:     string(azureSecret.Data[AzureStorageAccountNameSecretKey]),
			Container:   stg.Azure.Container,
			EndpointURL: stg.Azure.EndpointURL,
			Prefix:      stg.Azure.Prefix,
			Credentials: azure.Credentials{
				Key: string(azureSecret.Data[AzureStorageAccountKeySecretKey]),
			},
		},
	}

	return storageConf, nil
}

func GetPBMStorageConfig(
	ctx context.Context,
	k8sclient client.Client,
	cluster *psmdbv1.PerconaServerMongoDB,
	stg psmdbv1.BackupStorageSpec,
) (config.StorageConf, error) {
	pbm210Plus, err := cluster.ComparePBMAgentVersion("2.10.0")
	if err != nil {
		return config.StorageConf{}, errors.Wrap(err, "compare pbm-agent version")
	}

	switch stg.Type {
	case psmdbv1.BackupStorageS3:
		if pbm210Plus >= 0 && strings.Contains(stg.S3.EndpointURL, naming.GCSEndpointURL) {
			conf, err := GetPBMStorageS3CompatibleGCSConfig(ctx, k8sclient, cluster, stg)
			return conf, errors.Wrap(err, "get s3-compatible gcs config")
		}
		conf, err := GetPBMStorageS3Config(ctx, k8sclient, cluster, stg)
		return conf, errors.Wrap(err, "get s3 config")
	case psmdbv1.BackupStorageMinio:
		conf, err := GetPBMStorageMinioConfig(ctx, k8sclient, cluster, stg)
		return conf, errors.Wrap(err, "get minio config")
	case psmdbv1.BackupStorageGCS:
		conf, err := GetPBMStorageGCSConfig(ctx, k8sclient, cluster, stg)
		return conf, errors.Wrap(err, "get gcs config")
	case psmdbv1.BackupStorageAzure:
		conf, err := GetPBMStorageAzureConfig(ctx, k8sclient, cluster, stg)
		return conf, errors.Wrap(err, "get azure config")
	case psmdbv1.BackupStorageFilesystem:
		return config.StorageConf{
			Type: storage.Filesystem,
			Filesystem: &fs.Config{
				Path: stg.Filesystem.Path,
			},
		}, nil
	default:
		return config.StorageConf{}, errors.New("unsupported backup storage type")
	}
}

func GetPBMProfile(
	ctx context.Context,
	k8sclient client.Client,
	cluster *psmdbv1.PerconaServerMongoDB,
	name string,
	stg psmdbv1.BackupStorageSpec,
) (config.Config, error) {
	stgConf, err := GetPBMStorageConfig(ctx, k8sclient, cluster, stg)
	if err != nil {
		return config.Config{}, errors.Wrap(err, "get external storage config")
	}

	return config.Config{
		Name:      name,
		Storage:   stgConf,
		IsProfile: true,
	}, nil
}

func (b *pbmC) AddProfile(
	ctx context.Context,
	k8sclient client.Client,
	cluster *psmdbv1.PerconaServerMongoDB,
	name string,
	stg psmdbv1.BackupStorageSpec,
) error {
	log := logf.FromContext(ctx)

	profile, err := GetPBMProfile(ctx, k8sclient, cluster, name, stg)
	if err != nil {
		return errors.Wrap(err, "get profile")
	}

	log.Info("Adding profile", "name", name, "type", profile.Storage.Type)

	if err := config.AddProfile(ctx, b.Client, &profile); err != nil {
		return errors.Wrap(err, "add profile")
	}

	return nil
}

func (b *pbmC) GetProfile(ctx context.Context, name string) (*config.Config, error) {
	return config.GetProfile(ctx, b.Client, name)
}

func (b *pbmC) RemoveProfile(ctx context.Context, name string) error {
	log := logf.FromContext(ctx)

	log.Info("Removing profile", "name", name)

	return config.RemoveProfile(ctx, b.Client, name)
}

// GetNSetConfigLegacy sets the PBM config with given storage
func (b *pbmC) GetNSetConfigLegacy(ctx context.Context, k8sclient client.Client, cluster *psmdbv1.PerconaServerMongoDB, stg psmdbv1.BackupStorageSpec) error {
	log := logf.FromContext(ctx)

	conf, err := GetPBMConfig(ctx, k8sclient, cluster, stg)
	if err != nil {
		return errors.Wrap(err, "get PBM config")
	}

	log.Info("Setting config", "cluster", cluster.Name, "storage", stg)

	if err := config.SetConfig(ctx, b.Client, &conf); err != nil {
		return errors.Wrap(err, "write config")
	}

	time.Sleep(11 * time.Second) // give time to init new storage

	return nil
}

// GetNSetConfig sets the PBM config with main storage defined in the cluster CR
func (b *pbmC) GetNSetConfig(ctx context.Context, k8sclient client.Client, cluster *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	mainStgName, mainStg, err := cluster.Spec.Backup.MainStorage()
	if err != nil {
		return errors.Wrap(err, "get main storage")
	}

	conf, err := GetPBMConfig(ctx, k8sclient, cluster, mainStg)
	if err != nil {
		return errors.Wrap(err, "get PBM config")
	}

	log.Info("Setting config", "cluster", cluster.Name, "mainStorage", mainStgName)

	if err := config.SetConfig(ctx, b.Client, &conf); err != nil {
		return errors.Wrap(err, "write config")
	}

	for name, stg := range cluster.Spec.Backup.Storages {
		if name == mainStgName {
			continue
		}

		if err := b.AddProfile(ctx, k8sclient, cluster, name, stg); err != nil {
			return errors.Wrap(err, "add profile")
		}
	}

	// if main storage is changed we need to remove it from profiles list
	// otherwise PBM will duplicate backup metadata
	if _, err := b.GetProfile(ctx, mainStgName); err == nil {
		err := b.RemoveProfile(ctx, mainStgName)
		if err != nil {
			return errors.Wrapf(err, "remove profile %s", mainStgName)
		}
	}

	return nil
}

func (b *pbmC) SetConfig(ctx context.Context, cfg *config.Config) error {
	err := config.SetConfig(ctx, b.Client, cfg)
	return errors.Wrap(err, "set config")
}

func (b *pbmC) ValidateBackup(ctx context.Context, cfg *config.Config, bcp *psmdbv1.PerconaServerMongoDBBackup) error {
	if err := b.ValidateBackupInMetadata(ctx, bcp); err != nil {
		return errors.Wrap(err, "validate backup in metadata")
	}

	if err := b.ValidateBackupInStorage(ctx, cfg, bcp); err != nil {
		return errors.Wrap(err, "validate backup in storage")
	}

	return nil
}

func (b *pbmC) ValidateBackupInMetadata(ctx context.Context, bcp *psmdbv1.PerconaServerMongoDBBackup) error {
	_, err := b.GetBackupMeta(ctx, bcp.Status.PBMname)
	if err != nil {
		return errors.Wrap(err, "get backup meta")
	}

	return nil
}

func (b *pbmC) ValidateBackupInStorage(ctx context.Context, cfg *config.Config, bcp *psmdbv1.PerconaServerMongoDBBackup) error {
	if cfg.Storage.Type == storage.Filesystem {
		return nil
	}

	e := b.Logger().NewEvent(string(ctrl.CmdRestore), "", "", primitive.Timestamp{})
	stg, err := util.StorageFromConfig(&cfg.Storage, "", e)
	if err != nil {
		return errors.Wrap(err, "storage from config")
	}

	backupName := bcp.Status.PBMname
	m, err := restore.GetMetaFromStore(stg, backupName)
	if err != nil {
		return errors.Wrap(err, "get backup metadata from storage")
	}

	if err := backup.CheckBackupFiles(ctx, stg, m.Name); err != nil {
		return errors.Wrap(err, "check backup files")
	}

	return nil
}

func (b *pbmC) AgentStatuses(ctx context.Context, hosts []string) ([]topo.AgentStat, error) {
	var agents []topo.AgentStat

	filter := bson.M{}
	if len(hosts) > 0 {
		filter = bson.M{"n": bson.M{"$in": hosts}}
	}

	col := b.AgentsStatusCollection()
	cur, err := col.Find(ctx, filter)
	if err != nil {
		return agents, errors.Wrap(err, "get agent statuses")
	}
	defer cur.Close(ctx) // nolint:errcheck

	if err := cur.All(ctx, &agents); err != nil {
		return agents, errors.Wrap(err, "decode agent statuses")
	}

	return agents, nil
}

func (b *pbmC) Conn() *mongo.Client {
	return b.MongoClient()
}

// Close close the PBM connection
func (b *pbmC) Close(ctx context.Context) error {
	return b.Disconnect(ctx)
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

func IsResync(l lock.LockHeader) bool {
	return l.Type == ctrl.CmdResync
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
	nodeInfo, err := topo.GetNodeInfo(context.TODO(), b.MongoClient())
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

func (b *pbmC) GetTimelinesPITR(ctx context.Context, rsMap map[string]string) ([]oplog.Timeline, error) {
	var (
		now       = primitive.Timestamp{T: uint32(time.Now().UTC().Unix())}
		timelines [][]oplog.Timeline
	)

	shards, err := topo.ClusterMembers(ctx, b.MongoClient())
	if err != nil {
		return nil, errors.Wrap(err, "getting cluster members")
	}

	reverseMap := util.MakeReverseRSMapFunc(rsMap)
	for _, s := range shards {
		rs := reverseMap(s.RS)

		rsTimelines, err := oplog.PITRGetValidTimelines(ctx, b.Client, rs, now)
		if err != nil {
			return nil, errors.Wrapf(err, "getting timelines for %s", rs)
		}

		timelines = append(timelines, rsTimelines)
	}

	return oplog.MergeTimelines(timelines...), nil
}

func (b *pbmC) GetLatestTimelinePITR(ctx context.Context, rsMap map[string]string) (oplog.Timeline, error) {
	timelines, err := b.GetTimelinesPITR(ctx, rsMap)
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

func (b *pbmC) GetPITRChunkContains(ctx context.Context, unixTS int64, rsMap map[string]string) (*oplog.OplogChunk, error) {
	nodeInfo, err := topo.GetNodeInfo(ctx, b.MongoClient())
	if err != nil {
		return nil, errors.Wrap(err, "getting node information")
	}

	reverseMap := util.MakeReverseRSMapFunc(rsMap)
	rs := reverseMap(nodeInfo.SetName)

	c, err := b.pitrGetChunkContains(ctx, rs, primitive.Timestamp{T: uint32(unixTS)})
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrNoOplogsForPITR
		}
		return nil, errors.Wrapf(err, "get PITR chunk that contains %d for %s", unixTS, rs)
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

	return lock.Node, nil
}

func (b *pbmC) GetStorage(ctx context.Context, e pbmLog.LogEvent) (storage.Storage, error) {
	return util.GetStorage(ctx, b.Client, "", e)
}

func (b *pbmC) GetConfig(ctx context.Context) (*config.Config, error) {
	return config.GetConfig(ctx, b.Client)
}

func (b *pbmC) GetConfigVar(ctx context.Context, key string) (any, error) {
	return config.GetConfigVar(ctx, b.Client, key)
}

func (b *pbmC) SetConfigVar(ctx context.Context, key, val string) error {
	return config.SetConfigVar(ctx, b.Client, key, val)
}

func (b *pbmC) GetBackupMeta(ctx context.Context, bcpName string) (*backup.BackupMeta, error) {
	return backup.NewDBManager(b.Client).GetBackupByName(ctx, bcpName)
}

// deleteBackup deletes backup with the given name from the current storage and pbm database
// deleteBackup, deleteBackupImpl and deleteIncremetalChainImpl is copied from PBM v2.11.0 to fix PBM-1633
// we need to stop maintaining these in operator v1.24.0
func deleteBackup(ctx context.Context, conn connect.Client, name, node string, event pbmLog.LogEvent) error {
	bcp, err := backup.NewDBManager(conn).GetBackupByName(ctx, name)
	if err != nil {
		return errors.Wrap(err, "get backup meta")
	}

	if bcp.Type == defs.IncrementalBackup {
		return deleteIncremetalChainImpl(ctx, conn, bcp, node, event)
	}

	return deleteBackupImpl(ctx, conn, bcp, node, event)
}

func deleteBackupImpl(
	ctx context.Context,
	conn connect.Client,
	bcp *BackupMeta,
	node string,
	event pbmLog.LogEvent,
) error {
	err := backup.CanDeleteBackup(ctx, conn, bcp)
	if err != nil {
		return err
	}

	conf := bcp.Store.StorageConf
	if conf.Type == storage.S3 && strings.Contains(conf.S3.EndpointURL, naming.GCSEndpointURL) {
		conf = config.StorageConf{
			Type: storage.GCS,
			GCS:  getGCSFromS3CompatibleConfig(conf.S3),
		}
	}

	stg, err := util.StorageFromConfig(&conf, node, event)
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	err = backup.DeleteBackupFiles(stg, bcp.Name)
	if err != nil {
		return errors.Wrap(err, "delete files from storage")
	}

	_, err = conn.BcpCollection().DeleteOne(ctx, bson.M{"name": bcp.Name})
	if err != nil {
		return errors.Wrap(err, "delete metadata from db")
	}

	return nil
}

func deleteIncremetalChainImpl(ctx context.Context, conn connect.Client, bcp *BackupMeta, node string, event pbmLog.LogEvent) error {
	increments, err := backup.FetchAllIncrements(ctx, conn, bcp)
	if err != nil {
		return err
	}

	err = backup.CanDeleteIncrementalChain(ctx, conn, bcp, increments)
	if err != nil {
		return err
	}

	all := []*BackupMeta{bcp}
	for _, bcps := range increments {
		all = append(all, bcps...)
	}

	conf := bcp.Store.StorageConf
	if conf.Type == storage.S3 && strings.Contains(conf.S3.EndpointURL, naming.GCSEndpointURL) {
		conf = config.StorageConf{
			Type: storage.GCS,
			GCS:  getGCSFromS3CompatibleConfig(conf.S3),
		}
	}

	stg, err := util.StorageFromConfig(&conf, node, event)
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	for i := len(all) - 1; i >= 0; i-- {
		bcp := all[i]

		err = backup.DeleteBackupFiles(stg, bcp.Name)
		if err != nil {
			return errors.Wrap(err, "delete files from storage")
		}

		_, err = conn.BcpCollection().DeleteOne(ctx, bson.M{"name": bcp.Name})
		if err != nil {
			return errors.Wrap(err, "delete metadata from db")
		}

	}

	return nil
}

func (b *pbmC) DeleteBackup(ctx context.Context, name string) error {
	e := b.Logger().NewEvent(string(ctrl.CmdDeleteBackup), "", "", primitive.Timestamp{})
	return deleteBackup(ctx, b.Client, name, "", e)
}

func (b *pbmC) GetRestoreMeta(ctx context.Context, name string) (*restore.RestoreMeta, error) {
	return restore.GetRestoreMeta(ctx, b.Client, name)
}

func (b *pbmC) ResyncMainStorage(ctx context.Context) error {
	return b.SendCmd(ctx, ctrl.Cmd{Cmd: ctrl.CmdResync})
}

func (b *pbmC) ResyncMainStorageAndWait(ctx context.Context) error {
	if err := b.ResyncMainStorage(ctx); err != nil {
		return errors.Wrap(err, "start resync")
	}

	if err := b.WaitForResync(ctx); err != nil {
		return errors.Wrap(err, "wait for resync")
	}

	return nil
}

func (b *pbmC) WaitForResync(ctx context.Context) error {
	log := logf.FromContext(ctx)

	startCtx, startCancel := context.WithTimeout(ctx, 30*time.Second)
	defer startCancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	wait_resync_start := func() error {
		for {
			select {
			case <-startCtx.Done():
				return errors.New("resync is not started until deadline")
			case <-ticker.C:
				resyncRunning, err := b.HasLocks(startCtx, IsResync)
				if err != nil {
					return errors.Wrap(err, "check PBM locks")
				}

				if resyncRunning {
					return nil
				}
			}
		}
	}

	log.Info("waiting for resync to start (up to 30 seconds)")
	if err := wait_resync_start(); err != nil {
		return errors.Wrap(err, "wait for resync to start")
	}

	ticker.Reset(1 * time.Second)

	finishCtx, finishCancel := context.WithTimeout(ctx, 2*time.Hour)
	defer finishCancel()

	wait_resync_finish := func() error {
		for {
			select {
			case <-finishCtx.Done():
				return errors.New("resync is not finished until deadline")
			case <-ticker.C:
				resyncRunning, err := b.HasLocks(finishCtx, IsResync)
				if err != nil {
					return errors.Wrap(err, "check PBM locks")
				}

				if !resyncRunning {
					return nil
				}

				log.V(1).Info("resync is running")
			}
		}
	}

	log.Info("waiting for resync to finish (up to 2 hours)")
	if err := wait_resync_finish(); err != nil {
		return errors.Wrap(err, "wait for resync to finish")
	}

	log.Info("resync finished")

	return nil
}

func (b *pbmC) SendCmd(ctx context.Context, cmd ctrl.Cmd) error {
	cmd.TS = time.Now().UTC().Unix()
	_, err := b.CmdStreamCollection().InsertOne(ctx, cmd)
	return err
}

func (b *pbmC) PITRChunksCollection() *mongo.Collection {
	return b.Client.PITRChunksCollection()
}

func (b *pbmC) DeletePITRChunks(ctx context.Context, until primitive.Timestamp) error {
	e := b.Logger().NewEvent(string(ctrl.CmdDeletePITR), "", "", primitive.Timestamp{})

	cfg, err := b.GetConfig(ctx)
	if err != nil {
		return errors.Wrap(err, "get config")
	}

	stgConf := cfg.Storage
	if stgConf.Type == storage.S3 && strings.Contains(stgConf.S3.EndpointURL, naming.GCSEndpointURL) {
		stgConf = config.StorageConf{
			Type: storage.GCS,
			GCS:  getGCSFromS3CompatibleConfig(stgConf.S3),
		}
	}

	stg, err := util.StorageFromConfig(&stgConf, "", e)
	if err != nil {
		return errors.Wrap(err, "storage from config")
	}

	chunks, err := b.PITRGetChunksSlice(ctx, "", primitive.Timestamp{}, until)
	if err != nil {
		return errors.Wrap(err, "get pitr chunks")
	}
	if len(chunks) == 0 {
		return nil
	}

	for _, chnk := range chunks {
		err = stg.Delete(chnk.FName)
		if err != nil && err != storage.ErrNotExist {
			return errors.Wrapf(err, "delete pitr chunk '%s' (%v) from storage", chnk.FName, chnk)
		}

		_, err = b.PITRChunksCollection().DeleteOne(
			ctx,
			bson.D{
				{Key: "rs", Value: chnk.RS},
				{Key: "start_ts", Value: chnk.StartTS},
				{Key: "end_ts", Value: chnk.EndTS},
			},
		)
		if err != nil {
			return errors.Wrap(err, "delete pitr chunk metadata")
		}
	}
	return nil
}

func ResyncConfigExec(ctx context.Context, cl *clientcmd.Client, pod *corev1.Pod) error {
	log := logf.FromContext(ctx)

	stdoutBuffer := bytes.Buffer{}
	stderrBuffer := bytes.Buffer{}

	command := []string{"pbm", "config", "--force-resync"}

	log.Info("starting config resync", "pod", pod.Name, "command", command)

	err := cl.Exec(ctx, pod, naming.ContainerBackupAgent, command, nil, &stdoutBuffer, &stderrBuffer, false)
	if err != nil {
		return errors.Wrapf(err, "start resync: run %v stderr: %s stdout: %s", command, stderrBuffer.String(), stdoutBuffer.String())
	}

	return nil
}

func (b *pbmC) ResyncProfile(ctx context.Context, name string) error {
	opts := &ctrl.ResyncCmd{}
	if name == "all" {
		opts.All = true
	} else {
		opts.Name = name
	}

	cmd := ctrl.Cmd{
		Cmd:    ctrl.CmdResync,
		Resync: opts,
	}

	return b.SendCmd(ctx, cmd)
}

func (b *pbmC) ResyncProfileAndWait(ctx context.Context, name string) error {
	if err := b.ResyncProfile(ctx, name); err != nil {
		return errors.Wrap(err, "add profile")
	}

	if err := b.WaitForResync(ctx); err != nil {
		return errors.Wrap(err, "wait for resync")
	}

	return nil
}
