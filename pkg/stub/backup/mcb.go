package backup

import (
	"strings"

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

// MCBConfig represents the backup section of the config file for mongodb_consistent_backup
// See: https://github.com/Percona-Lab/mongodb_consistent_backup/blob/master/conf/mongodb-consistent-backup.example.conf#L14
type MCBConfigBackup struct {
	Name     string `yaml:"name"`
	Location string `yaml:"location"`
}

// MCBConfig represents the config file for mongodb_consistent_backup
// See: https://github.com/Percona-Lab/mongodb_consistent_backup/blob/master/conf/mongodb-consistent-backup.example.conf
type MCBConfig struct {
	Host     string           `yaml:"host"`
	Username string           `yaml:"username"`
	Password string           `yaml:"password"`
	Backup   *MCBConfigBackup `yaml:"backup"`
	Verbose  bool             `yaml:"verbose,omitempty"`
}

func (c *Controller) newMCBConfigYAML(replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret) (string, error) {
	addrs := getReplsetAddrs(c.psmdb, replset, pods)
	config := map[string]*MCBConfig{
		"production": &MCBConfig{
			Host:     replset.Name + "/" + strings.Join(addrs, ","),
			Username: string(usersSecret.Data[motPkg.EnvMongoDBBackupUser]),
			Password: string(usersSecret.Data[motPkg.EnvMongoDBBackupPassword]),
			Backup: &MCBConfigBackup{
				Name:     c.psmdb.Name,
				Location: "/data",
			},
			Verbose: c.psmdb.Spec.Backup.Verbose,
		},
	}
	bytes, err := yaml.Marshal(config)
	return string(bytes), err
}
