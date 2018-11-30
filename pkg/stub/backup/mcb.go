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
	return fmt.Sprintf("mongodb+srv://%s:%s@%s-%s.%s.svc.cluster.local/admin?ssl=false&replicaSet=%s",
		string(c.usersSecret.Data[motPkg.EnvMongoDBBackupUser]),
		string(c.usersSecret.Data[motPkg.EnvMongoDBBackupPassword]),
		c.psmdb.Name,
		replset.Name,
		c.psmdb.Namespace,
		replset.Name,
	)
}

func (c *Controller) newMCBConfigYAML(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod) ([]byte, error) {
	config := &MCBConfig{
		Host: c.getMongoDBURI(replset),
		Backup: &MCBConfigBackup{
			Name:     backup.Name,
			Location: "/data",
		},
		Verbose: backup.Verbose,
	}
	if backup.ArchiveMode != v1alpha1.BackupArchiveModeNone {
		config.Archive = &MCBConfigArchive{
			Method: backup.ArchiveMode,
		}
	}
	//if backup.Rotate != nil {
	//	config.Rotate = &MCBConfigRotate{
	//		MaxBackups: backup.MaxBackups,
	//		MaxDays:    backup.MaxDays,
	//	}
	//}
	data := map[string]*MCBConfig{"production": config}
	return yaml.Marshal(data)
}
