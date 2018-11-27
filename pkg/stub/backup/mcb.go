package backup

import (
	"fmt"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	yaml "gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
)

const (
	backupImagePrefix  = "perconalab/mongodb_consistent_backup"
	backupImageVersion = "1.3.0-3.6"
	backupConfigFile   = "config.yaml"
)

var (
	backupConfigFileMode = int32(0060)
)

type MCBArchiveMethod string

var (
	MCBArchiveMethodTar  MCBArchiveMethod = "tar"
	MCBArchiveMethodNone MCBArchiveMethod = "none"
)

type MCBConfigArchive struct {
	Method v1alpha1.BackupArchiveMode `yaml:"method,omitempty"`
}

// MCBConfig represents the backup section of the config file for mongodb_consistent_backup
// See: https://github.com/Percona-Lab/mongodb_consistent_backup/blob/master/conf/mongodb-consistent-backup.example.conf#L14
type MCBConfigBackup struct {
	Name     string `yaml:"name"`
	Location string `yaml:"location"`
}

type MCBConfigRotate struct {
	MaxBackups int `yaml:"max_backups,omitempty"`
	MaxDays    int `yaml:"max_days,omitempty"`
}

// MCBConfig represents the config file for mongodb_consistent_backup
// See: https://github.com/Percona-Lab/mongodb_consistent_backup/blob/master/conf/mongodb-consistent-backup.example.conf
type MCBConfig struct {
	Host    string            `yaml:"host"`
	Archive *MCBConfigArchive `yaml:"archive,omitempty"`
	Backup  *MCBConfigBackup  `yaml:"backup,omitempty"`
	Rotate  *MCBConfigRotate  `yaml:"rotate,omitempty"`
	Verbose bool              `yaml:"verbose,omitempty"`
}

func (c *Controller) getMongoDBURI(replset *v1alpha1.ReplsetSpec) string {
	return fmt.Sprintf("mongodb+srv://%s:%s@%s-%s.%s.svc.cluster.local/admin?ssl=false",
		string(usersSecret.Data[motPkg.EnvMongoDBBackupUser]),
		string(usersSecret.Data[motPkg.EnvMongoDBBackupPassword]),
		c.psmdb.Name,
		replset.Name,
		c.psmdb.Namespace,
		host,
	)
}

func (c *Controller) newMCBConfigYAML(replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret) ([]byte, error) {
	config := &MCBConfig{
		Host: c.getMongoDBURI(replset),
		Backup: &MCBConfigBackup{
			Name:     c.psmdb.Name,
			Location: "/data",
		},
		Verbose: c.psmdb.Spec.Backup.Verbose,
	}
	if c.psmdb.Spec.Backup.ArchiveMode != v1alpha1.BackupArchiveModeNone {
		config.Archive = &MCBConfigArchive{
			Method: c.psmdb.Spec.Backup.ArchiveMode,
		}
	}
	if c.psmdb.Spec.Backup.Rotate != nil {
		config.Rotate = &MCBConfigRotate{
			MaxBackups: c.psmdb.Spec.Backup.MaxBackups,
			MaxDays:    c.psmdb.Spec.Backup.MaxDays,
		}
	}
	data := map[string]*MCBConfig{"production": config}
	return yaml.Marshal(data)
}
