package naming

import (
	"fmt"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

// ClusterSyncLeaseName is the cluster-busy announcement held by an
// active ClusterSync CR. Backups and restores check it before starting
// and wait in Waiting state while it is held by any holder.
func ClusterSyncLeaseName(clusterName string) string {
	return "psmdb-" + clusterName + "-clustersync-lock"
}

func ClusterSyncHolderId(cr *psmdbv1.PerconaServerMongoDBClusterSync) string {
	return fmt.Sprintf("%s-%s", cr.Name, cr.UID)
}
