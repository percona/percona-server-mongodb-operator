package pbm

import (
	"bytes"
	"context"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

// SetConfigFile sets the PBM configuration file
func SetConfigFile(ctx context.Context, path string) error {
	return nil
}

// SetConfigKey sets the PBM configuration key
func SetConfigVar(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, key, value string) error {
	return nil
}

// ForceResync forces a resync of the PBM storage
func ForceResync(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod) error {
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"pbm", "config", "--force-resync"}

	err := exec(ctx, cli, pod, cmd, &stdout, &stderr)
	if err != nil {
		return err
	}

	return nil
}

func GetConfig(ctx context.Context, k8sclient client.Client, cr *psmdbv1.PerconaServerMongoDB, stg psmdbv1.BackupStorageSpec) (config.Config, error) {
	cnf := config.Config{
		PITR: config.PITRConf{
			Enabled:          cr.Spec.Backup.PITR.Enabled,
			OplogSpanMin:     cr.Spec.Backup.PITR.OplogSpanMin.Float64(),
			OplogOnly:        cr.Spec.Backup.PITR.OplogOnly,
			Compression:      cr.Spec.Backup.PITR.CompressionType,
			CompressionLevel: cr.Spec.Backup.PITR.CompressionLevel,
		},
	}

	switch stg.Type {
	case storage.S3:
		creds, err := GetS3Crendentials(ctx, k8sclient, cr.Namespace, stg.S3)
		if err != nil {
			return cnf, errors.Wrap(err, "get S3 credentials")
		}
		cnf.Storage = config.StorageConf{
			Type: storage.S3,
			S3:   NewS3Config(stg.S3, creds),
		}
	case storage.Azure:
		account, creds, err := GetAzureCrendentials(ctx, k8sclient, cr.Namespace, stg.Azure)
		if err != nil {
			return cnf, errors.Wrap(err, "get Azure credentials")
		}
		cnf.Storage = config.StorageConf{
			Type:  storage.Azure,
			Azure: NewAzureConfig(stg.Azure, account, creds),
		}
	}

	return cnf, nil
}

func CreateOrUpdateConfig(ctx context.Context, k8sclient client.Client, cr *psmdbv1.PerconaServerMongoDB, stg psmdbv1.BackupStorageSpec) error {
	cnf, err := GetConfig(ctx, k8sclient, cr, stg)
	if err != nil {
		return errors.Wrap(err, "get config")
	}

	cnfBytes, err := yaml.Marshal(cnf)
	if err != nil {
		return errors.Wrap(err, "marshal config")
	}

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-pbm-config",
			Namespace: cr.Namespace,
		},
		Data: map[string][]byte{
			"config.yaml": cnfBytes,
		},
	}

	err = k8sclient.Get(ctx, client.ObjectKeyFromObject(&secret), &secret)
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			err = k8sclient.Create(ctx, &secret)
			if err != nil {
				return errors.Wrap(err, "create secret")
			}
			return nil
		}

		return errors.Wrap(err, "get secret")
	}

	err = k8sclient.Update(ctx, &secret)
	if err != nil {
		return errors.Wrap(err, "update secret")
	}

	return nil
}
