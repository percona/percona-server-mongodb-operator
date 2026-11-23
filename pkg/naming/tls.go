package naming

const (
	CertificateOrganizationRootCA = "Root CA"
	CertificateOrganizationPSMDB  = "PSMDB"
	CertificateOrganizationPBM    = "PBM"
	PBMClientTLSVolumeName        = "pbm-tls"
	PBMClientTLSDirectory         = "/etc/mongodb-pbm"
	// PBMClientTLSPEMFile must differ from mongod's /tmp/tls.pem: with readOnlyRootFilesystem
	// the mongod and backup-agent containers share one /tmp volume.
	PBMClientTLSPEMFile = "/tmp/pbm-tls.pem"
)
