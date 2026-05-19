package vectorsearch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/vectorsearch/mongot"
)

func newTestCR() *api.PerconaServerMongoDB {
	return &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "psmdb",
			Namespace: "default",
		},
		Spec: api.PerconaServerMongoDBSpec{
			ClusterServiceDNSSuffix: "svc.cluster.local",
		},
	}
}

func newTestCRSharded() *api.PerconaServerMongoDB {
	cr := newTestCR()
	cr.Spec.Sharding.Enabled = true
	cr.Spec.Sharding.Mongos = &api.MongosSpec{
		Size: 3,
	}
	cr.Spec.Sharding.ConfigsvrReplSet = &api.ReplsetSpec{
		Name: "cfg",
		Size: 3,
	}

	return cr
}

func newTestRS() *api.ReplsetSpec {
	return &api.ReplsetSpec{
		Name: "rs0",
		Size: 3,
	}
}

// tlsDisabledSearch returns a SearchSpec that overrides default config and disables TLS
func tlsDisabledSearch() *api.SearchSpec {
	return &api.SearchSpec{
		Enabled: true,
		Configuration: `
server:
  grpc:
    tls:
      mode: Disabled
`,
	}
}

func TestSearchHost(t *testing.T) {
	cr := newTestCR()
	rs := newTestRS()

	assert.Equal(t,
		"psmdb-rs0-search-0.psmdb-rs0-search.default.svc.cluster.local:27028",
		SearchHost(cr, rs),
	)
}

func TestSearchHost_OtherClusterAndNamespace(t *testing.T) {
	cr := newTestCR()
	cr.Name = "my-cluster"
	cr.Namespace = "my-ns"
	cr.Spec.ClusterServiceDNSSuffix = "svc.example.test"
	rs := newTestRS()
	rs.Name = "shard-1"

	assert.Equal(t,
		"my-cluster-shard-1-search-0.my-cluster-shard-1-search.my-ns.svc.example.test:27028",
		SearchHost(cr, rs),
	)
}

func TestInjectMongodConfig_SearchDisabled_ReturnsUserConfigUnchanged(t *testing.T) {
	cr := newTestCR()
	// cr.Spec.Search left nil → IsSearchEnabled() is false
	rs := newTestRS()

	in := "net:\n  port: 27017\n"
	out, err := InjectMongodConfig(in, cr, rs)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestInjectMongodConfig_ConfigSvr_ReturnsUserConfigUnchanged(t *testing.T) {
	cr := newTestCR()
	cr.Spec.Search = tlsDisabledSearch()
	rs := newTestRS()
	rs.ClusterRole = api.ClusterRoleConfigSvr

	in := "net:\n  port: 27017\n"
	out, err := InjectMongodConfig(in, cr, rs)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestInjectMongodConfig_OverlaysOperatorKeys(t *testing.T) {
	cr := newTestCR()
	cr.Spec.Search = &api.SearchSpec{Enabled: true}
	rs := newTestRS()

	out, err := InjectMongodConfig("", cr, rs)
	require.NoError(t, err)

	parsed := map[string]any{}
	require.NoError(t, yaml.Unmarshal([]byte(out), &parsed))

	setParam, ok := parsed["setParameter"].(map[any]any)
	require.True(t, ok, "setParameter must be a mapping")

	host := "psmdb-rs0-search-0.psmdb-rs0-search.default.svc.cluster.local:27028"
	assert.Equal(t, host, setParam["mongotHost"])
	assert.Equal(t, host, setParam["searchIndexManagementHostAndPort"])
	assert.Equal(t, true, setParam["useGrpcForSearch"])
	assert.Equal(t, "requireTLS", setParam["searchTLSMode"])
}

func TestInjectMongodConfig_OperatorKeysWinOverUserSetParameter(t *testing.T) {
	cr := newTestCR()
	cr.Spec.Search = tlsDisabledSearch()
	rs := newTestRS()

	in := `
setParameter:
  mongotHost: user-attempt:9999
  useGrpcForSearch: false
  searchTLSMode: requireTLS
  unrelatedTuning: 42
`
	out, err := InjectMongodConfig(in, cr, rs)
	require.NoError(t, err)

	parsed := map[string]any{}
	require.NoError(t, yaml.Unmarshal([]byte(out), &parsed))

	setParam, ok := parsed["setParameter"].(map[any]any)
	require.True(t, ok)

	host := "psmdb-rs0-search-0.psmdb-rs0-search.default.svc.cluster.local:27028"
	assert.Equal(t, host, setParam["mongotHost"], "operator must overwrite user-provided mongotHost")
	assert.Equal(t, true, setParam["useGrpcForSearch"], "operator must overwrite user-provided useGrpcForSearch")
	assert.Equal(t, "disabled", setParam["searchTLSMode"], "operator must overwrite user-provided searchTLSMode")
	assert.Equal(t, 42, setParam["unrelatedTuning"], "unrelated setParameter keys must be preserved")
}

func TestInjectMongodConfig_PreservesUnrelatedTopLevelKeys(t *testing.T) {
	cr := newTestCR()
	cr.Spec.Search = tlsDisabledSearch()
	rs := newTestRS()

	in := `
net:
  port: 27017
storage:
  dbPath: /data/db
`
	out, err := InjectMongodConfig(in, cr, rs)
	require.NoError(t, err)

	parsed := map[string]any{}
	require.NoError(t, yaml.Unmarshal([]byte(out), &parsed))

	net, ok := parsed["net"].(map[any]any)
	require.True(t, ok)
	assert.Equal(t, 27017, net["port"])

	storage, ok := parsed["storage"].(map[any]any)
	require.True(t, ok)
	assert.Equal(t, "/data/db", storage["dbPath"])
}

func TestInjectMongodConfig_InvalidYAMLReturnsError(t *testing.T) {
	cr := newTestCR()
	cr.Spec.Search = tlsDisabledSearch()
	rs := newTestRS()

	_, err := InjectMongodConfig("net: [unterminated\n", cr, rs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse mongod configuration")
}

func TestInjectMongosConfig_SearchDisabled_ReturnsUserConfigUnchanged(t *testing.T) {
	cr := newTestCRSharded()
	// cr.Spec.Search left nil

	in := "net:\n  port: 27017\n"
	out, err := InjectMongosConfig(in, cr)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestInjectMongosConfig_NotSharded_ReturnsUserConfigUnchanged(t *testing.T) {
	cr := newTestCR()
	cr.Spec.Search = tlsDisabledSearch()
	// cr.Spec.Sharding.Enabled is false

	in := "net:\n  port: 27017\n"
	out, err := InjectMongosConfig(in, cr)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestInjectMongosConfig_ShardedSetsHostsPerShard(t *testing.T) {
	cr := newTestCRSharded()
	cr.Spec.Search = tlsDisabledSearch()
	cr.Spec.Replsets = []*api.ReplsetSpec{
		{Name: "rs0", Size: 3},
		{Name: "rs1", Size: 3},
		{Name: "cfg", Size: 3, ClusterRole: api.ClusterRoleConfigSvr},
	}

	out, err := InjectMongosConfig("", cr)
	require.NoError(t, err)

	parsed := map[string]any{}
	require.NoError(t, yaml.Unmarshal([]byte(out), &parsed))

	setParam, ok := parsed["setParameter"].(map[any]any)
	require.True(t, ok)

	host, _ := setParam["searchIndexManagementHostAndPort"]
	assert.Equal(t, "psmdb-rs0-search-0.psmdb-rs0-search.default.svc.cluster.local:27028", host)
	assert.Equal(t, true, setParam["useGrpcForSearch"])
	assert.Equal(t, "disabled", setParam["searchTLSMode"])
}

func TestInjectMongosConfig_PreservesUnrelatedSetParameterKeys(t *testing.T) {
	cr := newTestCRSharded()
	cr.Spec.Search = tlsDisabledSearch()
	cr.Spec.Replsets = []*api.ReplsetSpec{
		{Name: "rs0", Size: 3},
	}

	in := `
setParameter:
  taskExecutorPoolSize: 4
`
	out, err := InjectMongosConfig(in, cr)
	require.NoError(t, err)

	parsed := map[string]any{}
	require.NoError(t, yaml.Unmarshal([]byte(out), &parsed))

	setParam, ok := parsed["setParameter"].(map[any]any)
	require.True(t, ok)
	assert.Equal(t, 4, setParam["taskExecutorPoolSize"])
}

func TestInjectMongosConfig_InvalidYAMLReturnsError(t *testing.T) {
	cr := newTestCRSharded()
	cr.Spec.Search = tlsDisabledSearch()
	cr.Spec.Replsets = []*api.ReplsetSpec{{Name: "rs0", Size: 3}}

	_, err := InjectMongosConfig("net: [unterminated\n", cr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse mongos configuration")
}

func newDefaultExpectedConfig() mongot.Config {
	return mongot.Config{
		SyncSource: mongot.ConfigSyncSource{
			ReplicaSet: mongot.ConfigReplicaSet{
				HostAndPort: []string{
					"psmdb-rs0-0.psmdb-rs0.default.svc.cluster.local:27017",
					"psmdb-rs0-1.psmdb-rs0.default.svc.cluster.local:27017",
					"psmdb-rs0-2.psmdb-rs0.default.svc.cluster.local:27017",
				},
				Username:     "searchCoordinator",
				PasswordFile: "/etc/users-secret/MONGODB_SEARCH_PASSWORD",
			},
		},
		Storage: mongot.ConfigStorage{
			DataPath: "/data/mongot",
		},
		Server: mongot.ConfigServer{
			Grpc: &mongot.ConfigGrpc{
				Address: "0.0.0.0:27028",
				TLS: &mongot.ConfigGrpcTLS{
					Mode:                     mongot.ConfigTLSModeMTLS,
					CertificateKeyFile:       new(mongotTLSCertificatePath),
					CertificateAuthorityFile: new(mongotTLSCAPath),
				},
			},
		},
		Metrics: mongot.ConfigMetrics{
			Address: "0.0.0.0:9946",
		},
		HealthCheck: mongot.ConfigHealthCheck{
			Address: "0.0.0.0:8080",
		},
		Logging: mongot.ConfigLogging{
			Verbosity: "INFO",
		},
	}
}

func TestDefaultConfig(t *testing.T) {
	tests := map[string]struct {
		mutateCR func(*api.PerconaServerMongoDB)
		mutateRS func(*api.ReplsetSpec)
		expected mongot.Config
	}{
		"non-sharded replset with default size": {
			expected: newDefaultExpectedConfig(),
		},
		"non-sharded replset with size 1": {
			mutateRS: func(rs *api.ReplsetSpec) { rs.Size = 1 },
			expected: func() mongot.Config {
				cfg := newDefaultExpectedConfig()
				cfg.SyncSource.ReplicaSet.HostAndPort = []string{
					"psmdb-rs0-0.psmdb-rs0.default.svc.cluster.local:27017",
				}
				return cfg
			}(),
		},
		"non-sharded replset with custom port from rs configuration": {
			mutateRS: func(rs *api.ReplsetSpec) {
				rs.Configuration = api.MongoConfiguration(`
net:
  port: 27117
`)
			},
			expected: func() mongot.Config {
				cfg := newDefaultExpectedConfig()
				cfg.SyncSource.ReplicaSet.HostAndPort = []string{
					"psmdb-rs0-0.psmdb-rs0.default.svc.cluster.local:27117",
					"psmdb-rs0-1.psmdb-rs0.default.svc.cluster.local:27117",
					"psmdb-rs0-2.psmdb-rs0.default.svc.cluster.local:27117",
				}
				return cfg
			}(),
		},
		"sharded cluster routes through router (replicaSet hosts unset)": {
			mutateCR: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Sharding.Enabled = true
				cr.Spec.Sharding.Mongos = &api.MongosSpec{
					Size: 3,
				}
				cr.Spec.Sharding.ConfigsvrReplSet = &api.ReplsetSpec{
					Name: "cfg",
					Size: 3,
				}
			},
			expected: func() mongot.Config {
				cfg := newDefaultExpectedConfig()
				cfg.SyncSource.Router = &mongot.ConfigRouter{
					HostAndPort:  []string{"psmdb-mongos.default.svc.cluster.local:27017"},
					Username:     "searchCoordinator",
					PasswordFile: "/etc/users-secret/MONGODB_SEARCH_PASSWORD",
				}
				return cfg
			}(),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := newTestCR()
			rs := newTestRS()
			if tt.mutateCR != nil {
				tt.mutateCR(cr)
			}
			if tt.mutateRS != nil {
				tt.mutateRS(rs)
			}

			got := defaultMongotConfig(cr, rs)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestUserConfig(t *testing.T) {
	tests := map[string]struct {
		userConfig     string
		expectedConfig mongot.Config
	}{
		"empty configuration returns defaults": {
			userConfig:     "",
			expectedConfig: newDefaultExpectedConfig(),
		},
		"override logging verbosity preserves other defaults": {
			userConfig: `
logging:
  verbosity: DEBUG
`,
			expectedConfig: func() mongot.Config {
				cfg := newDefaultExpectedConfig()
				cfg.Logging.Verbosity = "DEBUG"
				return cfg
			}(),
		},
		"override storage dataPath preserves other defaults": {
			userConfig: `
storage:
  dataPath: /var/lib/mongot
`,
			expectedConfig: func() mongot.Config {
				cfg := newDefaultExpectedConfig()
				cfg.Storage.DataPath = "/var/lib/mongot"
				return cfg
			}(),
		},
		"override metrics enabled preserves address": {
			userConfig: `
metrics:
  enabled: true
`,
			expectedConfig: func() mongot.Config {
				cfg := newDefaultExpectedConfig()
				cfg.Metrics.Enabled = true
				return cfg
			}(),
		},
		"override hostAndPort preserves username and passwordFile": {
			userConfig: `
syncSource:
  replicaSet:
    hostAndPort:
      - mongod.example.com:27017
`,
			expectedConfig: func() mongot.Config {
				cfg := newDefaultExpectedConfig()
				cfg.SyncSource.ReplicaSet.HostAndPort = []string{"mongod.example.com:27017"}
				return cfg
			}(),
		},
		"override server grpc address preserves other defaults": {
			userConfig: `
server:
  grpc:
    address: 127.0.0.1:27028
`,
			expectedConfig: func() mongot.Config {
				cfg := newDefaultExpectedConfig()
				cfg.Server.Grpc.Address = "127.0.0.1:27028"
				return cfg
			}(),
		},
		"add embedding section preserves all defaults": {
			userConfig: `
embedding:
  queryKeyFile: /etc/keys/query
  indexingKeyFile: /etc/keys/indexing
  providerEndpoint: https://embed.example.com
  isAutoEmbeddingViewWriter: true
`,
			expectedConfig: func() mongot.Config {
				cfg := newDefaultExpectedConfig()
				cfg.Embedding = &mongot.EmbeddingConfig{
					QueryKeyFile:              "/etc/keys/query",
					IndexingKeyFile:           "/etc/keys/indexing",
					ProviderEndpoint:          "https://embed.example.com",
					IsAutoEmbeddingViewWriter: new(true),
				}
				return cfg
			}(),
		},
		"override multiple sections combines overlays": {
			userConfig: `
logging:
  verbosity: TRACE
metrics:
  enabled: true
  address: 0.0.0.0:9000
`,
			expectedConfig: func() mongot.Config {
				cfg := newDefaultExpectedConfig()
				cfg.Logging.Verbosity = "TRACE"
				cfg.Metrics.Enabled = true
				cfg.Metrics.Address = "0.0.0.0:9000"
				return cfg
			}(),
		},
		"override logPath sets pointer field": {
			userConfig: `
logging:
  logPath: /var/log/mongot/mongot.log
`,
			expectedConfig: func() mongot.Config {
				cfg := newDefaultExpectedConfig()
				p := "/var/log/mongot/mongot.log"
				cfg.Logging.LogPath = &p
				return cfg
			}(),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := newTestCR()
			rs := newTestRS()
			cr.Spec.Search = &api.SearchSpec{
				Enabled:       true,
				Configuration: tt.userConfig,
			}

			got, err := userMongotConfig(cr, rs)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedConfig, got)
		})
	}
}

func TestUserConfig_NilSearchSpec(t *testing.T) {
	cr := newTestCR()
	rs := newTestRS()
	// cr.Spec.Search left nil
	got, err := userMongotConfig(cr, rs)
	require.NoError(t, err)
	assert.Equal(t, newDefaultExpectedConfig(), got)
}

func TestUserConfig_InvalidYAML(t *testing.T) {
	cr := newTestCR()
	rs := newTestRS()
	cr.Spec.Search = &api.SearchSpec{
		Enabled: true,
		Configuration: `
logging:
 verbosity: [unterminated
`,
	}

	_, err := userMongotConfig(cr, rs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse spec.search.configuration")
}
