package pbm

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"
	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

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

func getMongoUri(ctx context.Context, k8sclient client.Client, cr *psmdbv1.PerconaServerMongoDB, addrs []string) (string, error) {
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

func NewLegacyClient(ctx context.Context, k8sclient client.Client, cluster *psmdbv1.PerconaServerMongoDB) (connect.Client, error) {
	rs := cluster.Spec.Replsets[0]

	pods, err := psmdb.GetRSPods(ctx, k8sclient, cluster, rs.Name)
	if err != nil {
		return nil, errors.Wrapf(err, "get pods list for replset %s", rs.Name)
	}

	if len(cluster.Spec.ClusterServiceDNSSuffix) == 0 {
		cluster.Spec.ClusterServiceDNSSuffix = psmdbv1.DefaultDNSSuffix
	}

	addrs, err := psmdb.GetReplsetAddrs(ctx, k8sclient, cluster, cluster.Spec.ClusterServiceDNSMode, rs.Name, false, pods.Items)
	if err != nil {
		return nil, errors.Wrap(err, "get replset addrs")
	}

	murl, err := getMongoUri(ctx, k8sclient, cluster, addrs)
	if err != nil {
		return nil, errors.Wrap(err, "get mongo uri")
	}

	pbmc, err := connect.Connect(ctx, murl, &connect.ConnectOptions{AppName: "operator-pbm-ctl"})
	if err != nil {
		return nil, errors.Wrapf(err, "create PBM connection to %s", strings.Join(addrs, ","))
	}

	return pbmc, nil
}

// GetPriorities returns priorities to be used in PBM config.
func GetPriorities(ctx context.Context, k8sclient client.Client, cluster *psmdbv1.PerconaServerMongoDB) (map[string]float64, error) {
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

func GetPBMConfig(ctx context.Context, k8sclient client.Client, cluster *psmdbv1.PerconaServerMongoDB, stg psmdbv1.BackupStorageSpec) (config.Config, error) {
	conf := config.Config{}

	priority, err := GetPriorities(ctx, k8sclient, cluster)
	if err != nil {
		return conf, errors.Wrap(err, "get priorities")
	}

	conf = config.Config{
		PITR: config.PITRConf{
			Enabled:          cluster.Spec.Backup.PITR.Enabled,
			Compression:      cluster.Spec.Backup.PITR.CompressionType,
			CompressionLevel: cluster.Spec.Backup.PITR.CompressionLevel,
		},
		Backup: config.BackupConf{
			Priority: priority,
		},
	}

	switch stg.Type {
	case storage.S3:
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

		if len(stg.S3.ServerSideEncryption.SSECustomerAlgorithm) != 0 && len(stg.S3.ServerSideEncryption.SSECustomerKey) != 0 {
			conf.Storage.S3.ServerSideEncryption = &s3.AWSsse{
				SseCustomerAlgorithm: stg.S3.ServerSideEncryption.SSECustomerAlgorithm,
				SseCustomerKey:       stg.S3.ServerSideEncryption.SSECustomerKey,
			}
		}

		if len(stg.S3.ServerSideEncryption.SSEAlgorithm) != 0 && len(stg.S3.ServerSideEncryption.KMSKeyID) != 0 {
			conf.Storage.S3.ServerSideEncryption = &s3.AWSsse{
				SseAlgorithm: stg.S3.ServerSideEncryption.SSEAlgorithm,
				KmsKeyID:     stg.S3.ServerSideEncryption.KMSKeyID,
			}
		}

		if len(stg.S3.CredentialsSecret) != 0 {
			s3secret, err := getSecret(ctx, k8sclient, cluster.Namespace, stg.S3.CredentialsSecret)
			if err != nil {
				return conf, errors.Wrap(err, "get s3 credentials secret")
			}

			conf.Storage.S3.Credentials = s3.Credentials{
				AccessKeyID:     string(s3secret.Data[AWSAccessKeyID]),
				SecretAccessKey: string(s3secret.Data[AWSSecretAccessKey]),
			}
		}
	case storage.Azure:
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
				Account:   string(azureSecret.Data[AzureStorageAccountName]),
				Container: stg.Azure.Container,
				Prefix:    stg.Azure.Prefix,
				Credentials: azure.Credentials{
					Key: string(azureSecret.Data[AzureStorageAccountKey]),
				},
			},
		}
	case storage.Filesystem:
		return conf, errors.New("filesystem backup storage not supported yet, skipping storage name")
	default:
		return conf, errors.New("unsupported backup storage type")
	}

	return conf, nil
}

func LegacySetConfig(ctx context.Context, k8sclient client.Client, cluster *psmdbv1.PerconaServerMongoDB, stg psmdbv1.BackupStorageSpec) error {
	pbmc, err := NewLegacyClient(ctx, k8sclient, cluster)
	if err != nil {
		return errors.Wrap(err, "create legacy PBM client")
	}

	conf, err := GetPBMConfig(ctx, k8sclient, cluster, stg)
	if err != nil {
		return errors.Wrap(err, "get PBM config")
	}

	if err := config.SetConfig(ctx, pbmc, conf); err != nil {
		return errors.Wrap(err, "set PBM config")
	}

	time.Sleep(11 * time.Second) // give time to init new storage
	return nil
}
