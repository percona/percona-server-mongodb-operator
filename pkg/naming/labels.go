package naming

import (
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

const (
	labelKubernetesPrefix = "app.kubernetes.io/"

	LabelKubernetesName      = labelKubernetesPrefix + "name"
	LabelKubernetesInstance  = labelKubernetesPrefix + "instance"
	LabelKubernetesManagedBy = labelKubernetesPrefix + "managed-by"
	LabelKubernetesPartOf    = labelKubernetesPrefix + "part-of"
	LabelKubernetesComponent = labelKubernetesPrefix + "component"
	LabelKubernetesReplset   = labelKubernetesPrefix + "replset"
)

const (
	LabelBackupAncestor = perconaPrefix + "backup-ancestor"
	LabelBackupType     = perconaPrefix + "backup-type"
	LabelCluster        = perconaPrefix + "cluster"
)

func ClusterLabels(cr *api.PerconaServerMongoDB) map[string]string {
	return map[string]string{
		LabelKubernetesName:      "percona-server-mongodb",
		LabelKubernetesInstance:  cr.Name,
		LabelKubernetesManagedBy: "percona-server-mongodb-operator",
		LabelKubernetesPartOf:    "percona-server-mongodb",
	}
}

func ServiceLabels(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) map[string]string {
	return RSLabels(cr, replset)
}

func ExternalServiceLabels(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) map[string]string {
	ls := RSLabels(cr, replset)
	ls[LabelKubernetesComponent] = "external-service"

	return ls
}

func MongodLabels(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) map[string]string {
	ls := RSLabels(cr, replset)
	ls[LabelKubernetesComponent] = "mongod"
	return ls
}

func ArbiterLabels(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) map[string]string {
	ls := RSLabels(cr, replset)
	ls[LabelKubernetesComponent] = "arbiter"
	return ls
}

func NonVotingLabels(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) map[string]string {
	ls := RSLabels(cr, replset)
	ls[LabelKubernetesComponent] = "nonVoting"
	return ls
}

func MongosLabels(cr *api.PerconaServerMongoDB) map[string]string {
	ls := ClusterLabels(cr)
	ls[LabelKubernetesComponent] = "mongos"
	return ls
}

func RSLabels(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) map[string]string {
	ls := ClusterLabels(cr)
	if replset != nil {
		ls[LabelKubernetesReplset] = replset.Name
	}
	return ls
}

func ScheduledBackupLabels(cr *api.PerconaServerMongoDB, task *api.BackupTaskSpec) map[string]string {
	if cr.CompareVersion("1.17.0") < 0 {
		return map[string]string{
			"ancestor": task.Name,
			"cluster":  cr.Name,
			"type":     "cron",
		}
	}
	ls := ClusterLabels(cr)
	ls[LabelBackupAncestor] = task.Name
	ls[LabelCluster] = cr.Name
	ls[LabelBackupType] = "cron"

	return ls
}

func NewBackupCronJobLabels(cr *api.PerconaServerMongoDB, labels map[string]string) map[string]string {
	ls := ClusterLabels(cr)
	ls[LabelKubernetesReplset] = "general"
	ls[LabelKubernetesComponent] = "backup-schedule"

	ls = util.MapMerge(util.MapCopy(labels), ls)

	return ls
}
