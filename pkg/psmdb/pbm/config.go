package pbm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

const ConfigFileDir = "/etc/pbm"

// FileExists checks if a file exists in the PBM container
func FileExists(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, path string) bool {
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"test", "-f", path}

	err := exec(ctx, cli, pod, BackupAgentContainerName, cmd, nil, &stdout, &stderr)

	return err == nil
}

func GetConfigPathForStorage(name string) string {
	return fmt.Sprintf("%s/%s", ConfigFileDir, name)
}

// SetConfigFile sets the PBM configuration file
func SetConfigFile(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, path string) error {
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"pbm", "config", "--file", path}

	err := exec(ctx, cli, pod, BackupAgentContainerName, cmd, nil, &stdout, &stderr)
	if err != nil {
		return errors.Wrapf(err, "stdout: %s, stderr: %s", stdout.String(), stderr.String())
	}

	return nil
}

// SetConfigKey sets the PBM configuration key
func SetConfigVar(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, key, value string) error {
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"pbm", "config", fmt.Sprintf("--set=%s=%s", key, value)}

	err := exec(ctx, cli, pod, BackupAgentContainerName, cmd, nil, &stdout, &stderr)
	if err != nil {
		return errors.Wrapf(err, "stdout: %s, stderr: %s", stdout.String(), stderr.String())
	}

	return nil
}

// ForceResync forces a resync of the PBM storage
func ForceResync(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod) error {
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"pbm", "config", "--force-resync"}

	err := exec(ctx, cli, pod, BackupAgentContainerName, cmd, nil, &stdout, &stderr)
	if err != nil {
		return errors.Wrapf(err, "stdout: %s, stderr: %s", stdout.String(), stderr.String())
	}

	return nil
}

func calculateSHA256Sum(data map[string][]byte) string {
	keys := make([]string, 0, len(data))
	sums := make(map[string]string)
	for key, d := range data {
		keys = append(keys, key)
		sums[key] = fmt.Sprintf("%x", sha256.Sum256(d))
	}

	slices.Sort[[]string](keys)

	lines := make([]string, 0, len(sums))
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("%s  %s", sums[key], ConfigFileDir+"/"+key))
	}

	checkfile := strings.Join(lines, "\n") + "\n"

	return fmt.Sprintf("%x", sha256.Sum256([]byte(checkfile)))
}

// CheckSHA256Sum checks the SHA256 checksum of a file in the PBM container
func CheckSHA256Sum(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, checksum string) bool {
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"bash", "-c", "sha256sum " + ConfigFileDir + "/* | sha256sum | awk '{print $1}'"}

	err := exec(ctx, cli, pod, BackupAgentContainerName, cmd, nil, &stdout, &stderr)
	if err != nil {
		return false
	}

	return strings.TrimSpace(stdout.String()) == checksum
}

// GetConfigChecksum returns the SHA256 checksum of the *applied* PBM configuration
func GetConfigChecksum(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod) (string, error) {
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"pbm", "config"}

	err := exec(ctx, cli, pod, BackupAgentContainerName, cmd, nil, &stdout, &stderr)
	if err != nil {
		return "", errors.Wrapf(err, "stdout: %s, stderr: %s", stdout.String(), stderr.String())
	}

	sha256sum := fmt.Sprintf("%x", sha256.Sum256(stdout.Bytes()))

	return sha256sum, nil
}

// GenerateConfig generates a PBM configuration based on the PerconaServerMongoDB CR
func GenerateConfig(ctx context.Context, k8sclient client.Client, cr *psmdbv1.PerconaServerMongoDB) (config.Config, error) {
	cnf := config.Config{
		PITR: config.PITRConf{
			Enabled:          cr.Spec.Backup.PITR.Enabled,
			OplogSpanMin:     cr.Spec.Backup.PITR.OplogSpanMin.Float64(),
			OplogOnly:        cr.Spec.Backup.PITR.OplogOnly,
			Compression:      cr.Spec.Backup.PITR.CompressionType,
			CompressionLevel: cr.Spec.Backup.PITR.CompressionLevel,
		},
	}

	return cnf, nil
}

func NewStorageConfig(ctx context.Context, k8sclient client.Client, namespace string, stg psmdbv1.BackupStorageSpec) (config.StorageConf, error) {
	switch stg.Type {
	case storage.S3:
		creds, err := GetS3Crendentials(ctx, k8sclient, namespace, stg.S3)
		if err != nil {
			return config.StorageConf{}, err
		}

		return config.StorageConf{
			Type: storage.S3,
			S3:   NewS3Config(stg.S3, creds),
		}, nil

	case storage.Azure:
		account, creds, err := GetAzureCrendentials(ctx, k8sclient, namespace, stg.Azure)
		if err != nil {
			return config.StorageConf{}, err
		}

		return config.StorageConf{
			Type:  storage.Azure,
			Azure: NewAzureConfig(stg.Azure, account, creds),
		}, nil
	default:
		return config.StorageConf{}, errors.Errorf("unknown storage type %s", stg.Type)
	}
}

func SetStorageConfig(ctx context.Context, cli *clientcmd.Client, k8sclient client.Client, pod *corev1.Pod, stg psmdbv1.BackupStorageSpec) error {
	conf, err := NewStorageConfig(ctx, k8sclient, pod.Namespace, stg)
	if err != nil {
		return errors.Wrap(err, "get storage config")
	}

	type storageConf struct {
		Storage config.StorageConf `yaml:"storage"`
	}
	s := storageConf{Storage: conf}

	sBytes, err := yaml.Marshal(s)
	if err != nil {
		return errors.Wrap(err, "marshal storage config")
	}

	stdin := bytes.NewReader(sBytes)
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"pbm", "config", "--file=-"}

	err = exec(ctx, cli, pod, BackupAgentContainerName, cmd, stdin, &stdout, &stderr)
	if err != nil {
		return errors.Wrapf(err, "stdout: %s, stderr: %s", stdout.String(), stderr.String())
	}

	return nil
}

func CreateOrUpdateConfig(ctx context.Context, cli *clientcmd.Client, k8sclient client.Client, cr *psmdbv1.PerconaServerMongoDB) error {
	l := log.FromContext(ctx)

	cnf, err := GenerateConfig(ctx, k8sclient, cr)
	if err != nil {
		return errors.Wrap(err, "get config")
	}

	data := make(map[string][]byte)

	for name, st := range cr.Spec.Backup.Storages {
		conf, err := NewStorageConfig(ctx, k8sclient, cr.Namespace, st)
		if err != nil {
			return errors.Wrapf(err, "get storage config for %s", name)
		}

		cnf.Storage = conf

		stgBytes, err := yaml.Marshal(cnf)
		if err != nil {
			return errors.Wrapf(err, "marshal storage %s", name)
		}

		data[name] = stgBytes
	}

	sha256sum := calculateSHA256Sum(data)

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-pbm-config",
			Namespace: cr.Namespace,
		},
	}

	err = k8sclient.Get(ctx, client.ObjectKeyFromObject(&secret), &secret)
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			l.Info("Creating PBM config secret", "secret", secret.Name, "checksum", sha256sum)
			secret.Annotations = make(map[string]string)
			secret.Annotations[psmdbv1.AnnotationPBMConfigSum] = sha256sum
			secret.Data = data
			err = k8sclient.Create(ctx, &secret)
			if err != nil {
				return errors.Wrap(err, "create secret")
			}
			return nil
		}

		return errors.Wrap(err, "get secret")
	}

	checksum, ok := secret.Annotations[psmdbv1.AnnotationPBMConfigSum]
	if ok && checksum == sha256sum {
		return nil
	}

	l.Info("Updating PBM config secret", "secret", secret.Name, "checksum", sha256sum)

	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}
	delete(secret.Annotations, psmdbv1.AnnotationPBMConfigApplied)
	secret.Annotations[psmdbv1.AnnotationPBMConfigSum] = sha256sum

	secret.Data = data
	err = k8sclient.Update(ctx, &secret)
	if err != nil {
		return errors.Wrap(err, "update secret")
	}

	return nil
}

func EnablePITR(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod) error {
	return SetConfigVar(ctx, cli, pod, "pitr.enabled", "true")
}

func DisablePITR(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod) error {
	return SetConfigVar(ctx, cli, pod, "pitr.enabled", "false")
}
