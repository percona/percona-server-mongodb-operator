package naming

const perconaPrefix = "percona.com/"

const (
	FinalizerDeleteBackup           = perconaPrefix + "delete-backup"
	FinalizerDeletePVC              = perconaPrefix + "delete-psmdb-pvc"
	FinalizerDeletePSMDBPodsInOrder = perconaPrefix + "delete-psmdb-pods-in-order"
)
