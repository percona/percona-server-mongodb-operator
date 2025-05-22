package naming

import (
	"fmt"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func BackupLeaseName(clusterName string) string {
	return "psmdb-" + clusterName + "-backup-lock"
}

func BackupHolderId(cr *psmdbv1.PerconaServerMongoDBBackup) string {
	return fmt.Sprintf("%s-%s", cr.Name, cr.UID)
}
