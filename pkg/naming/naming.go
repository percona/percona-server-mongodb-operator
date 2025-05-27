package naming

import (
	"fmt"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

const (
	perconaPrefix  = "percona.com/"
	internalPrefix = "internal." + perconaPrefix
)

const (
	FinalizerDeleteBackup           = perconaPrefix + "delete-backup"
	FinalizerDeletePITR             = perconaPrefix + "delete-pitr-chunks"
	FinalizerDeletePVC              = perconaPrefix + "delete-psmdb-pvc"
	FinalizerDeletePSMDBPodsInOrder = perconaPrefix + "delete-psmdb-pods-in-order"
	FinalizerReleaseLock            = internalPrefix + "release-lock"
)

const (
	ComponentMongod         = "mongod"
	ComponentMongos         = "mongos"
	ComponentNonVoting      = "nonVoting"
	ComponentNonVotingShort = "nv"
	ComponentHidden         = "hidden"
	ComponentArbiter        = "arbiter"
)

const (
	ContainerBackupAgent = "backup-agent"
	ContainerMongod      = ComponentMongod
	ContainerMongos      = ComponentMongos
	ContainerNonVoting   = ContainerMongod + "-" + ComponentNonVotingShort
	ContainerArbiter     = ContainerMongod + "-" + ComponentArbiter
	ContainerHidden      = ContainerMongod + "-" + ComponentHidden
)

func MongodStatefulSetName(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec) string {
	return fmt.Sprintf("%s-%s", cr.Name, rs.Name)
}

func MongodCustomConfigName(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec) string {
	return fmt.Sprintf("%s-%s-%s", cr.Name, rs.Name, ComponentMongod)
}

func MongosCustomConfigName(cr *psmdbv1.PerconaServerMongoDB) string {
	return fmt.Sprintf("%s-%s", cr.Name, ComponentMongos)
}

func NonVotingStatefulSetName(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec) string {
	return fmt.Sprintf("%s-%s-%s", cr.Name, rs.Name, ComponentNonVotingShort)
}

func NonVotingConfigMapName(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec) string {
	return NonVotingStatefulSetName(cr, rs)
}

func HiddenStatefulSetName(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec) string {
	return fmt.Sprintf("%s-%s-%s", cr.Name, rs.Name, ComponentHidden)
}

func HiddenConfigMapName(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec) string {
	return HiddenStatefulSetName(cr, rs)
}

func ArbiterStatefulSetName(cr *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec) string {
	return fmt.Sprintf("%s-%s-%s", cr.Name, rs.Name, ComponentArbiter)
}
