package naming

const (
	AnnotationCancelBackup    = perconaPrefix + "cancel-backup"
	AnnotationRestoreName     = perconaPrefix + "restore-name"
	AnnotationSSLHash         = perconaPrefix + "ssl-hash"
	AnnotationSSLInternalHash = perconaPrefix + "ssl-internal-hash"
	AnnotationConfigHash      = perconaPrefix + "configuration-hash"
)

const AnnotationKubectlRestartedAt = "kubectl.kubernetes.io/restartedAt"

const (
	AnnotationExternalDNSHostname = "external-dns.alpha.kubernetes.io/hostname"
	AnnotationExternalDNSTTL      = "external-dns.alpha.kubernetes.io/ttl"
	AnnotationExternalDNSManaged  = perconaPrefix + "external-dns-managed"
)
