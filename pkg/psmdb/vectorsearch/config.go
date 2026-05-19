package vectorsearch

import (
	"fmt"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/vectorsearch/mongot"
)

// File paths referenced from inside the rendered mongot.conf. They
// match the volume mounts set up by StatefulSet in this package.
const (
	mongotTLSCAPath          = config.SSLDir + "/ca.crt"
	mongotTLSCertificatePath = "/tmp/tls.pem" // tls.key+tls.crt, concatenated in mongot entrypoint
)

// ConfigMap returns the ConfigMap that holds the operator-rendered
// mongot.conf for the given replset/shard. The single data key is
// ConfigFileName ("mongot.conf").
func ConfigMap(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) (*corev1.ConfigMap, error) {
	data, err := renderConfig(cr, rs)
	if err != nil {
		return nil, errors.Wrap(err, "render mongot.conf")
	}

	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.SearchConfigMapName(cr, rs),
			Namespace: cr.Namespace,
			Labels:    naming.SearchLabels(cr, rs),
		},
		Data: map[string]string{
			ConfigFileName: data,
		},
	}, nil
}

func renderConfig(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) (string, error) {
	cfg, err := userMongotConfig(cr, rs)
	if err != nil {
		return "", errors.Wrap(err, "get mongot.conf")
	}

	cfgB, err := yaml.Marshal(cfg)
	if err != nil {
		return "", errors.Wrap(err, "marshal config to yaml")
	}

	return string(cfgB), nil
}

// userMongotConfig returns the YAML body of mongot.conf for the given
// replset, with the user's configuration overlay applied. yaml.v2
// unmarshals into the existing struct in place, so only fields that
// appear in spec.Configuration are overridden — every other default
// is preserved.
func userMongotConfig(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) (mongot.Config, error) {
	cfg := defaultMongotConfig(cr, rs)

	spec := cr.Spec.Search
	if spec == nil || spec.Configuration == "" {
		return cfg, nil
	}

	if err := yaml.Unmarshal([]byte(spec.Configuration), &cfg); err != nil {
		return cfg, errors.Wrap(err, "parse spec.search.configuration")
	}

	return cfg, nil
}

// defaultMongotConfig returns the default mongot.conf.
func defaultMongotConfig(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) mongot.Config {
	cfg := mongot.Config{
		SyncSource: mongot.ConfigSyncSource{
			ReplicaSet: mongot.ConfigReplicaSet{
				Username:     string(api.RoleSearch),
				PasswordFile: UsersSecretMountPath + "/" + api.EnvMongoDBSearchPassword,
			},
		},
		Logging: mongot.ConfigLogging{
			Verbosity: "INFO",
		},
		Storage: mongot.ConfigStorage{
			DataPath: DataMountPath,
		},
		Server: mongot.ConfigServer{
			Grpc: &mongot.ConfigGrpc{
				TLS: &mongot.ConfigGrpcTLS{
					Mode: mongot.ConfigTLSModeDisabled,
				},
				Address: fmt.Sprintf("0.0.0.0:%d", GRPCPort),
			},
		},
		HealthCheck: mongot.ConfigHealthCheck{
			Address: fmt.Sprintf("0.0.0.0:%d", HealthCheckPort),
		},
		Metrics: mongot.ConfigMetrics{
			Address: fmt.Sprintf("0.0.0.0:%d", MetricsPort),
		},
	}

	if cr.TLSEnabled() {
		cfg.Server.Grpc.TLS = &mongot.ConfigGrpcTLS{
			Mode:                     mongot.ConfigTLSModeMTLS,
			CertificateKeyFile:       new(mongotTLSCertificatePath),
			CertificateAuthorityFile: new(mongotTLSCAPath),
		}
	}

	hosts := make([]string, rs.Size)
	for i := range rs.Size {
		hosts[i] = mongodHostAndPort(cr, rs, i)
	}
	cfg.SyncSource.ReplicaSet.HostAndPort = hosts

	if sharding := cr.Spec.Sharding; sharding.Enabled && sharding.Mongos != nil {
		cfg.SyncSource.Router = &mongot.ConfigRouter{
			HostAndPort:  mongosHostAndPort(cr),
			Username:     string(api.RoleSearch),
			PasswordFile: UsersSecretMountPath + "/" + api.EnvMongoDBSearchPassword,
		}
	}

	return cfg
}

// mongodHostAndPort returns the FQDN mongot uses to open its change-stream connection
// for the given pod idx
func mongodHostAndPort(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, idx int32) string {
	return fmt.Sprintf("%s-%s-%d.%s-%s.%s.%s:%d",
		cr.Name, rs.Name, idx, cr.Name, rs.Name, cr.Namespace, cr.Spec.ClusterServiceDNSSuffix, rs.GetPort())
}

// mongosHostAndPort returns all the FQDNs mongot uses to open its change-stream connection
func mongosHostAndPort(cr *api.PerconaServerMongoDB) []string {
	if mongos := cr.Spec.Sharding.Mongos; mongos.Expose.ServicePerPod {
		hosts := make([]string, mongos.Size)
		for i := range mongos.Size {
			hosts[i] = fmt.Sprintf("%s-%d.%s.%s:%d",
				naming.MongosStatefulSetName(cr), i, cr.Namespace, cr.Spec.ClusterServiceDNSSuffix, mongos.GetPort())
		}

		return hosts
	}

	return []string{fmt.Sprintf("%s.%s.%s:%d",
		naming.MongosServiceName(cr), cr.Namespace, cr.Spec.ClusterServiceDNSSuffix, cr.Spec.Sharding.Mongos.GetPort())}
}

// SearchHost returns the fully qualified address mongod uses to reach
// this replset's mongot —
// `<cluster>-<rs>-search-0.<cluster>-<rs>-search.<ns>.<dnsSuffix>:27028`.
func SearchHost(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) string {
	return fmt.Sprintf("%s-0.%s.%s.%s:%d",
		naming.SearchStatefulSetName(cr, rs), naming.SearchServiceName(cr, rs), cr.Namespace,
		cr.Spec.ClusterServiceDNSSuffix, GRPCPort)
}

func buildSearchSetParameters(mongotEndpoint string, tlsMode api.TLSMode) map[string]any {
	return map[string]any{
		"mongotHost":                                      mongotEndpoint,
		"searchIndexManagementHostAndPort":                mongotEndpoint,
		"skipAuthenticationToSearchIndexManagementServer": false,
		"skipAuthenticationToMongot":                      false,
		"searchTLSMode":                                   string(tlsMode),
		"useGrpcForSearch":                                true,
	}
}

func searchTLSMode(cfg mongot.Config) api.TLSMode {
	if cfg.Server.TLSEnabled() {
		return api.TLSModeRequire
	}

	return api.TLSModeDisabled
}

// InjectMongodConfig overlays the operator-managed `setParameter`
// block onto the user-supplied mongod configuration. The four keys
// the operator owns:
//   - mongotHost
//   - searchIndexManagementHostAndPort
//   - useGrpcForSearch
//   - searchTLSMode  (only when cluster TLS is enabled or the user
//     pinned `spec.search.tls.mode`)
//
// always win on conflict.
//
// Returns the user config unchanged when search is disabled or
// when the replset is the config server.
func InjectMongodConfig(userConfig string, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) (string, error) {
	if !cr.IsSearchEnabled() || rs.ClusterRole == api.ClusterRoleConfigSvr {
		return userConfig, nil
	}

	top := map[any]any{}
	if userConfig != "" {
		if err := yaml.Unmarshal([]byte(userConfig), &top); err != nil {
			return "", errors.Wrap(err, "parse mongod configuration")
		}
	}

	setParam, _ := top["setParameter"].(map[any]any)
	if setParam == nil {
		setParam = map[any]any{}
	}

	mongotCfg, err := userMongotConfig(cr, rs)
	if err != nil {
		return "", errors.Wrap(err, "get mongot.conf")
	}

	host := SearchHost(cr, rs)
	tlsMode := searchTLSMode(mongotCfg)

	for k, v := range buildSearchSetParameters(host, tlsMode) {
		setParam[k] = v
	}

	top["setParameter"] = setParam

	out, err := yaml.Marshal(top)
	if err != nil {
		return "", errors.Wrap(err, "marshal mongod configuration")
	}
	return string(out), nil
}

// InjectMongosConfig overlays the operator-managed `setParameter`
// block onto a sharded cluster's mongos configuration. mongos needs a
// cluster-wide view of the search-index catalog: createSearchIndex /
// dropSearchIndex / listSearchIndexes issued through mongos must reach
// the matching shard's mongot.
//
// Returns the user config unchanged when search is disabled or the
// cluster is not sharded.
func InjectMongosConfig(userConfig string, cr *api.PerconaServerMongoDB) (string, error) {
	if !cr.IsSearchEnabled() || !cr.Spec.Sharding.Enabled {
		return userConfig, nil
	}

	top := map[any]any{}
	if userConfig != "" {
		if err := yaml.Unmarshal([]byte(userConfig), &top); err != nil {
			return "", errors.Wrap(err, "parse mongos configuration")
		}
	}

	setParam, _ := top["setParameter"].(map[any]any)
	if setParam == nil {
		setParam = map[any]any{}
	}

	mongotCfg, err := userMongotConfig(cr, cr.Spec.Replsets[0])
	if err != nil {
		return "", errors.Wrap(err, "get mongot.conf")
	}

	host := SearchHost(cr, cr.Spec.Replsets[0])
	tlsMode := searchTLSMode(mongotCfg)

	for k, v := range buildSearchSetParameters(host, tlsMode) {
		setParam[k] = v
	}

	top["setParameter"] = setParam

	out, err := yaml.Marshal(top)
	if err != nil {
		return "", errors.Wrap(err, "marshal mongos configuration")
	}
	return string(out), nil
}
