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
	ReplicaSet               ConfigReplicaSet `json:"replicaSet" yaml:"replicaSet"`
	Router                   *ConfigRouter    `json:"router,omitempty" yaml:"router,omitempty"`
	CertificateAuthorityFile *string          `json:"caFile,omitempty" yaml:"caFile,omitempty"`
}

type ConfigRouter struct {
	HostAndPort  []string    `json:"hostAndPort" yaml:"hostAndPort"`
	Username     string      `json:"username,omitempty" yaml:"username,omitempty"`
	PasswordFile string      `json:"passwordFile,omitempty" yaml:"passwordFile,omitempty"`
	TLS          *bool       `json:"tls,omitempty" yaml:"tls,omitempty"`
	AuthSource   *string     `json:"authSource,omitempty" yaml:"authSource,omitempty"`
	X509         *ConfigX509 `json:"x509,omitempty" yaml:"x509,omitempty"`
}

type ConfigReplicaSet struct {
	HostAndPort    []string    `json:"hostAndPort" yaml:"hostAndPort"`
	Username       string      `json:"username,omitempty" yaml:"username,omitempty"`
	PasswordFile   string      `json:"passwordFile,omitempty" yaml:"passwordFile,omitempty"`
	TLS            *bool       `json:"tls,omitempty" yaml:"tls,omitempty"`
	ReadPreference *string     `json:"readPreference,omitempty" yaml:"readPreference,omitempty"`
	AuthSource     *string     `json:"authSource,omitempty" yaml:"authSource,omitempty"`
	X509           *ConfigX509 `json:"x509,omitempty" yaml:"x509,omitempty"`
}

type ConfigX509 struct {
	TLSCertificateKeyFile             *string `json:"tlsCertificateKeyFile,omitempty" yaml:"tlsCertificateKeyFile,omitempty"`
	TLSCertificateKeyFilePasswordFile *string `json:"tlsCertificateKeyFilePasswordFile,omitempty" yaml:"tlsCertificateKeyFilePasswordFile,omitempty"`
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
