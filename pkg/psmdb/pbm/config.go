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

const (
	ConfigFileDir          = "/etc/pbm"
	PhysicalRestorePBMPath = "/opt/percona/pbm"
)

// FileExists checks if a file exists in the PBM container
func (p *PBM) FileExists(ctx context.Context, path string) bool {
	cmd := []string{"test", "-f", path}

	return p.exec(ctx, cmd, nil, nil) == nil
}

func GetConfigPathForStorage(name string) string {
	return fmt.Sprintf("%s/%s", ConfigFileDir, name)
}

// SetConfigFile sets the PBM configuration file
func (p *PBM) SetConfigFile(ctx context.Context, path string) error {
	cmd := []string{p.pbmPath, "config", "--file", path}

	err := p.exec(ctx, cmd, nil, nil)
	if err != nil {
		return wrapExecError(err, cmd)
	}

	return nil
}

// SetConfigKey sets the PBM configuration key
func (p *PBM) SetConfigVar(ctx context.Context, key, value string) error {
	cmd := []string{p.pbmPath, "config", fmt.Sprintf("--set=%s=%s", key, value)}

	err := p.exec(ctx, cmd, nil, nil)
	if err != nil {
		return wrapExecError(err, cmd)
	}

	return nil
}

// ForceResync forces a resync of the PBM storage
func (p *PBM) ForceResync(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod) error {
	cmd := []string{p.pbmPath, "config", "--force-resync"}

	err := p.exec(ctx, cmd, nil, nil)
	if err != nil {
		return wrapExecError(err, cmd)
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
func (p *PBM) CheckSHA256Sum(ctx context.Context, checksum string) bool {
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"bash", "-c", "sha256sum " + ConfigFileDir + "/* | sha256sum | awk '{print $1}'"}

	err := p.execClient.Exec(ctx, p.pod, p.containerName, cmd, nil, &stdout, &stderr, false)
	if err != nil {
		return false
	}

	return strings.TrimSpace(stdout.String()) == checksum
}

// GetConfigChecksum returns the SHA256 checksum of the *applied* PBM configuration
func (p *PBM) GetConfigChecksum(ctx context.Context) (string, error) {
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{p.pbmPath, "config"}

	err := p.execClient.Exec(ctx, p.pod, p.containerName, cmd, nil, &stdout, &stderr, false)
	if err != nil {
		return "", errors.Wrapf(wrapExecError(err, cmd), "stdout: %s, stderr: %s", stdout.String(), stderr.String())
	}

	sha256sum := fmt.Sprintf("%x", sha256.Sum256(stdout.Bytes()))

	return sha256sum, nil
}

// GenerateConfig generates a PBM configuration based on the PerconaServerMongoDB CR
func GenerateConfig(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB) (config.Config, error) {
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

func (p *PBM) NewStorageConfig(ctx context.Context, stg psmdbv1.BackupStorageSpec) (config.StorageConf, error) {
	switch stg.Type {
	case storage.S3:
		creds, err := GetS3Crendentials(ctx, p.k8sClient, p.pod.Namespace, stg.S3)
		if err != nil {
			return config.StorageConf{}, err
		}

		return config.StorageConf{
			Type: storage.S3,
			S3:   NewS3Config(stg.S3, creds),
		}, nil

	case storage.Azure:
		account, creds, err := GetAzureCrendentials(ctx, p.k8sClient, p.pod.Namespace, stg.Azure)
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

func (p *PBM) SetStorageConfig(ctx context.Context, stg psmdbv1.BackupStorageSpec) error {
	conf, err := p.NewStorageConfig(ctx, stg)
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

	cmd := []string{"pbm", "config", "--file=-"}

	err = p.exec(ctx, cmd, stdin, nil)
	if err != nil {
		return wrapExecError(err, cmd)
	}

	return nil
}

func (p *PBM) CreateOrUpdateConfig(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB) error {
	l := log.FromContext(ctx)

	cnf, err := GenerateConfig(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "get config")
	}

	data := make(map[string][]byte)

	for name, st := range cr.Spec.Backup.Storages {
		conf, err := p.NewStorageConfig(ctx, st)
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

	err = p.k8sClient.Get(ctx, client.ObjectKeyFromObject(&secret), &secret)
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			l.Info("Creating PBM config secret", "secret", secret.Name, "checksum", sha256sum)
			secret.Annotations = make(map[string]string)
			secret.Annotations[psmdbv1.AnnotationPBMConfigSum] = sha256sum
			secret.Data = data
			err = p.k8sClient.Create(ctx, &secret)
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
	err = p.k8sClient.Update(ctx, &secret)
	if err != nil {
		return errors.Wrap(err, "update secret")
	}

	return nil
}

func (p *PBM) EnablePITR(ctx context.Context) error {
	return p.SetConfigVar(ctx, "pitr.enabled", "true")
}

func (p *PBM) DisablePITR(ctx context.Context) error {
	return p.SetConfigVar(ctx, "pitr.enabled", "false")
}
