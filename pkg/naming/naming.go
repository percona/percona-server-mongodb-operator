package naming

import (
	"fmt"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

const perconaPrefix = "percona.com/"

const (
	FinalizerDeleteBackup           = perconaPrefix + "delete-backup"
	FinalizerDeletePITR             = perconaPrefix + "delete-pitr-chunks"
	FinalizerDeletePVC              = perconaPrefix + "delete-psmdb-pvc"
	FinalizerDeletePSMDBPodsInOrder = perconaPrefix + "delete-psmdb-pods-in-order"
)

const (
	ContainerBackupAgent = "backup-agent"
)

const (
	ComponentMongod    = "mongod"
	ComponentMongos    = "mongos"
	ComponentNonVoting = "nonVoting"
	ComponentArbiter   = "arbiter"
)

func MongodStatefulSetName(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec) string {
	return fmt.Sprintf("%s-%s", cr.Name, rs.Name)
}

func MongosStatefulSetName(cr *psmdbv1.PerconaServerMongoDB) string {
	return fmt.Sprintf("%s-mongos", cr.Name)
}

func NonVotingStatefulSetName(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec) string {
	return fmt.Sprintf("%s-%s-nv", cr.Name, rs.Name)
}

func ArbiterStatefulSetName(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec) string {
	return fmt.Sprintf("%s-%s-arbiter", cr.Name, rs.Name)
}
