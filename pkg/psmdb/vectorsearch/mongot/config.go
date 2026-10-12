package mongot

type Config struct {
	SyncSource  ConfigSyncSource  `json:"syncSource" yaml:"syncSource"`
	Storage     ConfigStorage     `json:"storage" yaml:"storage"`
	Server      ConfigServer      `json:"server" yaml:"server"`
	Metrics     ConfigMetrics     `json:"metrics" yaml:"metrics"`
	HealthCheck ConfigHealthCheck `json:"healthCheck" yaml:"healthCheck"`
	Logging     ConfigLogging     `json:"logging" yaml:"logging"`
	Embedding   *EmbeddingConfig  `json:"embedding,omitempty" yaml:"embedding,omitempty"`
}

type EmbeddingConfig struct {
	QueryKeyFile              string `json:"queryKeyFile" yaml:"queryKeyFile,omitempty"`
	IndexingKeyFile           string `json:"indexingKeyFile" yaml:"indexingKeyFile,omitempty"`
	ProviderEndpoint          string `json:"providerEndpoint" yaml:"providerEndpoint,omitempty"`
	IsAutoEmbeddingViewWriter *bool  `json:"isAutoEmbeddingViewWriter" yaml:"isAutoEmbeddingViewWriter,omitempty"`
}

type ConfigSyncSource struct {
	ReplicaSet        ConfigReplicaSet         `json:"replicaSet" yaml:"replicaSet"`
	Router            *ConfigRouter            `json:"router,omitempty" yaml:"router,omitempty"`
	ReplicationReader *ConfigReplicationReader `json:"replicationReader,omitempty" yaml:"replicationReader,omitempty"`
}

type ConfigReplicationReader struct {
	ReadPreference *string       `json:"readPreference,omitempty" yaml:"readPreference,omitempty"`
	TagSets        [][]ConfigTag `json:"tagSets,omitempty" yaml:"tagSets,omitempty"`
}

type ConfigTag struct {
	Name  string `json:"name" yaml:"name,omitempty"`
	Value string `json:"value" yaml:"value,omitempty"`
}

type ScramAuthTLS struct {
	Enabled                           bool    `json:"enabled" yaml:"enabled,omitempty"`
	TLSCertificateKeyFile             *string `json:"tlsCertificateKeyFile,omitempty" yaml:"tlsCertificateKeyFile,omitempty"`
	TLSCertificateKeyFilePasswordFile *string `json:"tlsCertificateKeyFilePasswordFile,omitempty" yaml:"tlsCertificateKeyFilePasswordFile,omitempty"`
	CertificateAuthorityFile          *string `json:"caFile,omitempty" yaml:"caFile,omitempty"`
}

type ConfigScramAuth struct {
	Username     string        `json:"username" yaml:"username,omitempty"`
	PasswordFile string        `json:"passwordFile" yaml:"passwordFile,omitempty"`
	TLS          *ScramAuthTLS `json:"tls,omitempty" yaml:"tls,omitempty"`
	AuthSource   *string       `json:"authSource,omitempty" yaml:"authSource,omitempty"`
}

type ConfigX509 struct {
	CertificateAuthorityFile          *string `json:"caFile,omitempty" yaml:"caFile,omitempty"`
	TLSCertificateKeyFile             *string `json:"tlsCertificateKeyFile,omitempty" yaml:"tlsCertificateKeyFile,omitempty"`
	TLSCertificateKeyFilePasswordFile *string `json:"tlsCertificateKeyFilePasswordFile,omitempty" yaml:"tlsCertificateKeyFilePasswordFile,omitempty"`
}

type ConfigRouter struct {
	HostAndPort []string         `json:"hostAndPort" yaml:"hostAndPort"`
	X509        *ConfigX509      `json:"x509,omitempty" yaml:"x509,omitempty"`
	ScramAuth   *ConfigScramAuth `json:"scramAuth,omitempty" yaml:"scramAuth,omitempty"`
}

type ConfigReplicaSet struct {
	HostAndPort []string         `json:"hostAndPort" yaml:"hostAndPort"`
	X509        *ConfigX509      `json:"x509,omitempty" yaml:"x509,omitempty"`
	ScramAuth   *ConfigScramAuth `json:"scramAuth,omitempty" yaml:"scramAuth,omitempty"`
}

type ConfigStorage struct {
	DataPath string `json:"dataPath" yaml:"dataPath"`
}

type ConfigServer struct {
	Grpc *ConfigGrpc `json:"grpc,omitempty" yaml:"grpc,omitempty"`
}

func (s ConfigServer) TLSEnabled() bool {
	return s.Grpc != nil && s.Grpc.TLS != nil && s.Grpc.TLS.Mode != ConfigTLSModeDisabled
}

type ConfigGrpc struct {
	Address string         `json:"address" yaml:"address"`
	TLS     *ConfigGrpcTLS `json:"tls,omitempty" yaml:"tls,omitempty"`
}

type ConfigTLSMode string

const (
	ConfigTLSModeTLS      ConfigTLSMode = "TLS"
	ConfigTLSModeMTLS     ConfigTLSMode = "mTLS"
	ConfigTLSModeDisabled ConfigTLSMode = "Disabled"
)

type ConfigGrpcTLS struct {
	Mode                     ConfigTLSMode `json:"mode" yaml:"mode"`
	CertificateKeyFile       *string       `json:"certificateKeyFile,omitempty" yaml:"certificateKeyFile,omitempty"`
	CertificateAuthorityFile *string       `json:"caFile,omitempty" yaml:"caFile,omitempty"`
}

type ConfigMetrics struct {
	Enabled bool   `json:"enabled" yaml:"enabled"`
	Address string `json:"address" yaml:"address"`
}

type ConfigHealthCheck struct {
	Address string `json:"address" yaml:"address"`
}

type ConfigLogging struct {
	Verbosity string  `json:"verbosity" yaml:"verbosity"`
	LogPath   *string `json:"logPath,omitempty" yaml:"logPath,omitempty"`
}
