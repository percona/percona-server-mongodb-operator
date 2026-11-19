package naming

const (
	AnnotationCancelBackup    = perconaPrefix + "cancel-backup"
	AnnotationRestoreName     = perconaPrefix + "restore-name"
	AnnotationSSLHash         = perconaPrefix + "ssl-hash"
	AnnotationPBMTLSHash      = perconaPrefix + "pbm-tls-hash"
	AnnotationSSLInternalHash = perconaPrefix + "ssl-internal-hash"
	AnnotationConfigHash      = perconaPrefix + "configuration-hash"

	// AnnotationPendingRestarts on the internal users secret lists the containers
	// that must still restart to read changed system user credentials.
	AnnotationPendingRestarts = perconaPrefix + "pending-restarts"
)

const AnnotationKubectlRestartedAt = "kubectl.kubernetes.io/restartedAt"

const (
	AnnotationExternalDNSHostname = "external-dns.alpha.kubernetes.io/hostname"
	AnnotationExternalDNSTTL      = "external-dns.alpha.kubernetes.io/ttl"
	AnnotationExternalDNSManaged  = perconaPrefix + "external-dns-managed"
)
