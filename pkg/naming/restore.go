package naming

import (
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func BackupSourceProfileName(restore *psmdbv1.PerconaServerMongoDBRestore) string {
	return restore.Name + "-backup-source"
}
