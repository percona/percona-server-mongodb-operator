package naming

const (
	annotationPrefix = "percona.com/"
)

const (
	FinalizerDeleteBackup           = annotationPrefix + "delete-backup"
	FinalizerDeletePVC              = annotationPrefix + "delete-psmdb-pvc"
	FinalizerDeletePSMDBPodsInOrder = annotationPrefix + "delete-psmdb-pods-in-order"
)
