package naming

const perconaPrefix = "percona.com/"

const (
	FinalizerDeleteBackup           = perconaPrefix + "delete-backup"
	FinalizerDeletePITR             = perconaPrefix + "delete-pitr-chunks"
	FinalizerDeletePVC              = perconaPrefix + "delete-psmdb-pvc"
	FinalizerDeletePSMDBPodsInOrder = perconaPrefix + "delete-psmdb-pods-in-order"
)
