package psmdb

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/yaml"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestMongosLogrotateAddress(t *testing.T) {
	type testCase struct {
		name             string
		servicePerPod    bool
		collectorEnabled bool
		expectedHost     string
		expectedLoopback []corev1.HostAlias
	}

	for _, tc := range []testCase{
		{
			name:             "shared service resolves to localhost",
			collectorEnabled: true,
			expectedHost:     "cluster-mongos.ns.svc.example",
			expectedLoopback: []corev1.HostAlias{{IP: "127.0.0.1", Hostnames: []string{"cluster-mongos.ns.svc.example"}}},
		},
		{
			name:             "per pod services resolve to localhost",
			servicePerPod:    true,
			collectorEnabled: true,
			expectedHost:     "$(POD_NAME).ns.svc.example",
			expectedLoopback: []corev1.HostAlias{{IP: "127.0.0.1", Hostnames: []string{
				"cluster-mongos-0.ns.svc.example",
				"cluster-mongos-1.ns.svc.example",
				"cluster-mongos-2.ns.svc.example",
			}}},
		},
		{
			name: "disabled collector leaves host aliases unchanged",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cr, err := readDefaultCR("cluster", "ns")
			require.NoError(t, err)
			cr.Spec.ClusterServiceDNSSuffix = "svc.example"
			cr.Spec.LogCollector = &api.LogCollectorSpec{Enabled: tc.collectorEnabled, Image: "logs"}
			cr.Spec.TLS = &api.TLSSpec{Mode: api.TLSModeRequire, AllowInvalidCertificates: new(false)}
			cr.Spec.Sharding.Mongos.Configuration = "net:\n  port: 28017\n"
			cr.Spec.Sharding.Mongos.Expose.ServicePerPod = tc.servicePerPod
			cr.Spec.Sharding.Mongos.Size = 3
			customAliases := []corev1.HostAlias{{IP: "192.0.2.1", Hostnames: []string{"custom.example"}}}
			cr.Spec.Sharding.Mongos.HostAliases = customAliases
			require.NoError(t, cr.CheckNSetDefaults(t.Context(), version.PlatformKubernetes))

			template, err := MongosTemplateSpec(cr, cr.Spec.InitImage, logr.Discard(), StatefulConfigParams{}, []string{"cfg:27017"}, true)
			require.NoError(t, err)
			expectedAliases := append(tc.expectedLoopback, customAliases...)
			require.Equal(t, expectedAliases, template.Spec.HostAliases)
			require.Equal(t, customAliases, cr.Spec.Sharding.Mongos.HostAliases, "building the template must not modify the CR")

			index := slices.IndexFunc(template.Spec.Containers, func(container corev1.Container) bool {
				return container.Name == "logrotate"
			})
			if !tc.collectorEnabled {
				require.Equal(t, -1, index)
				return
			}
			require.NotEqual(t, -1, index, "logrotate container must be present")
			logrotate := template.Spec.Containers[index]

			require.Contains(t, logrotate.Env, corev1.EnvVar{Name: "MONGODB_HOST", Value: tc.expectedHost})
			require.Contains(t, logrotate.Env, corev1.EnvVar{Name: "MONGODB_PORT", Value: "28017"})
			require.Contains(t, logrotate.Env, corev1.EnvVar{Name: "MONGODB_TLS_ENABLED", Value: "true"})
			require.False(t, slices.ContainsFunc(logrotate.Env, func(variable corev1.EnvVar) bool {
				return variable.Name == "MONGODB_TLS_INSECURE"
			}), "strict TLS must not set MONGODB_TLS_INSECURE")
			require.Contains(t, logrotate.VolumeMounts, corev1.VolumeMount{Name: "ssl", MountPath: config.SSLDir, ReadOnly: true})

			cert := x509.Certificate{DNSNames: tls.GetCertificateSans(cr)}
			if !tc.servicePerPod {
				require.NoError(t, cert.VerifyHostname(tc.expectedHost))
				return
			}

			require.Equal(t, "POD_NAME", logrotate.Env[0].Name)
			require.Equal(t, "metadata.name", logrotate.Env[0].ValueFrom.FieldRef.FieldPath)
			for i := 0; i < int(cr.Spec.Sharding.Mongos.Size); i++ {
				podName := naming.MongosPerPodServiceName(cr, i)
				podHost := strings.ReplaceAll(tc.expectedHost, "$(POD_NAME)", podName)
				svcSpec := MongosServiceSpec(cr, podName)
				require.Equal(t, podName, svcSpec.Selector["statefulset.kubernetes.io/pod-name"])
				require.NoError(t, cert.VerifyHostname(podHost))

				customCert := x509.Certificate{DNSNames: []string{podHost}}
				require.NoError(t, customCert.VerifyHostname(podHost))
			}
		})
	}
}

func TestMongosService(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		expose       api.Expose
		podName      string
		expectedSpec corev1.ServiceSpec
	}{
		"ClusterIP": {
			expose: api.Expose{
				ServiceLabels: map[string]string{
					"percona.com/test": "label",
				},
				ServiceAnnotations: map[string]string{
					"percona.com/test": "annotation",
				},
				ExposeType: corev1.ServiceTypeClusterIP,
			},
			podName: "test-cr-mongos-0",
			expectedSpec: corev1.ServiceSpec{
				PublishNotReadyAddresses: false,
				Ports: []corev1.ServicePort{
					{
						Name:        "mongos",
						Port:        27017,
						TargetPort:  intstr.FromInt(27017),
						AppProtocol: new("mongo"),
					},
				},
				Selector: map[string]string{
					"app.kubernetes.io/component":  "mongos",
					"app.kubernetes.io/instance":   "test-cr",
					"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
					"app.kubernetes.io/name":       "percona-server-mongodb",
					"app.kubernetes.io/part-of":    "percona-server-mongodb",
				},
				Type: corev1.ServiceTypeClusterIP,
			},
		},
		"NodePort": {
			expose: api.Expose{
				ServiceLabels: map[string]string{
					"percona.com/test": "label",
				},
				ServiceAnnotations: map[string]string{
					"percona.com/test": "annotation",
				},
				ExposeType: corev1.ServiceTypeNodePort,
			},
			podName: "test-cr-mongos-0",
			expectedSpec: corev1.ServiceSpec{
				PublishNotReadyAddresses: false,
				Ports: []corev1.ServicePort{
					{
						Name:        "mongos",
						Port:        27017,
						TargetPort:  intstr.FromInt(27017),
						AppProtocol: new("mongo"),
					},
				},
				Selector: map[string]string{
					"app.kubernetes.io/component":  "mongos",
					"app.kubernetes.io/instance":   "test-cr",
					"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
					"app.kubernetes.io/name":       "percona-server-mongodb",
					"app.kubernetes.io/part-of":    "percona-server-mongodb",
				},
				Type:                  corev1.ServiceTypeNodePort,
				ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyCluster,
			},
		},
		"LoadBalancer": {
			expose: api.Expose{
				ServiceLabels: map[string]string{
					"percona.com/test": "label",
				},
				ServiceAnnotations: map[string]string{
					"percona.com/test": "annotation",
				},
				ExposeType:               corev1.ServiceTypeLoadBalancer,
				LoadBalancerClass:        new("eks.amazonaws.com/nlb"),
				LoadBalancerSourceRanges: []string{"10.0.0.0/16"},
			},
			podName: "test-cr-mongos-0",
			expectedSpec: corev1.ServiceSpec{
				PublishNotReadyAddresses: false,
				Ports: []corev1.ServicePort{
					{
						Name:        "mongos",
						Port:        27017,
						TargetPort:  intstr.FromInt(27017),
						AppProtocol: new("mongo"),
					},
				},
				Selector: map[string]string{
					"app.kubernetes.io/component":  "mongos",
					"app.kubernetes.io/instance":   "test-cr",
					"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
					"app.kubernetes.io/name":       "percona-server-mongodb",
					"app.kubernetes.io/part-of":    "percona-server-mongodb",
				},
				Type:                     corev1.ServiceTypeLoadBalancer,
				ExternalTrafficPolicy:    corev1.ServiceExternalTrafficPolicyLocal,
				LoadBalancerClass:        new("eks.amazonaws.com/nlb"),
				LoadBalancerSourceRanges: []string{"10.0.0.0/16"},
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{
				Name:      "test-cr",
				Namespace: "test-ns",
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Sharding: api.Sharding{
						Mongos: &api.MongosSpec{
							Expose: api.MongosExpose{
								Expose: tt.expose,
							},
						},
					},
				},
			}
			spec := MongosServiceSpec(cr, tt.podName)
			assert.Equal(t, tt.expectedSpec, spec)
		})
	}
}

func TestMongosContainer(t *testing.T) {
	t.Parallel()

	cr, err := readDefaultCR("test-cr", "test-ns")
	assert.NoError(t, err)

	err = cr.CheckNSetDefaults(t.Context(), version.PlatformKubernetes)
	assert.NoError(t, err)

	// Add custom environment variables and EnvFrom to test they are included in the container
	cr.Spec.Sharding.Mongos.Env = []corev1.EnvVar{
		{
			Name:  "CUSTOM_ENV_VAR",
			Value: "custom-value",
		},
		{
			Name:  "ANOTHER_ENV_VAR",
			Value: "another-value",
		},
	}
	cr.Spec.Sharding.Mongos.EnvFrom = []corev1.EnvFromSource{
		{
			ConfigMapRef: &corev1.ConfigMapEnvSource{
				Name:     "test-configmap",
				Optional: new(false),
			},
		},
		{
			SecretRef: &corev1.SecretEnvSource{
				Name:     "test-secret",
				Optional: new(true),
			},
		},
	}

	container, err := mongosContainer(cr, false, []string{"cfg-0.test-cr-cfg.test-ns.svc.cluster.local:27017"}, cr.KeyFileAuthEnabled())
	assert.NoError(t, err)

	// Basic container fields
	assert.Equal(t, "mongos", container.Name)
	assert.Equal(t, "perconalab/percona-server-mongodb-operator:main-mongod8.0", container.Image)
	assert.Equal(t, corev1.PullAlways, container.ImagePullPolicy)
	assert.Equal(t, "/data/db", container.WorkingDir)

	// Command
	assert.NotEmpty(t, container.Command)
	assert.Contains(t, container.Command[0], "ps-entry.sh")

	// Args - check for key mongos arguments
	assert.NotEmpty(t, container.Args)
	assert.Contains(t, container.Args, "mongos")
	assert.Contains(t, container.Args, "--bind_ip_all")
	assert.Contains(t, container.Args, "--configdb")
	assert.Contains(t, container.Args, "--relaxPermChecks")

	// Ports
	assert.Len(t, container.Ports, 1)
	assert.Equal(t, "mongos", container.Ports[0].Name)
	assert.Equal(t, int32(27017), container.Ports[0].ContainerPort)

	// Environment variables
	assert.NotEmpty(t, container.Env)
	assert.Equal(t, "MONGODB_PORT", container.Env[0].Name)
	assert.Equal(t, "27017", container.Env[0].Value)

	// Check custom environment variables are added (if version >= 1.22.0)
	envMap := make(map[string]string)
	for _, env := range container.Env {
		envMap[env.Name] = env.Value
	}
	if cr.CompareVersion("1.22.0") >= 0 {
		assert.Equal(t, "custom-value", envMap["CUSTOM_ENV_VAR"], "custom env var should be present")
		assert.Equal(t, "another-value", envMap["ANOTHER_ENV_VAR"], "another env var should be present")
	}

	// EnvFrom - check secret references (base ones)
	assert.NotEmpty(t, container.EnvFrom)
	baseEnvFromCount := 2

	// Custom EnvFrom should be added
	assert.GreaterOrEqual(t, len(container.EnvFrom), baseEnvFromCount+2, "should have base EnvFrom plus custom ones")

	// Check for custom ConfigMapRef
	foundConfigMap := false
	foundCustomSecret := false
	for _, envFrom := range container.EnvFrom {
		if envFrom.ConfigMapRef != nil && envFrom.ConfigMapRef.Name == "test-configmap" {
			foundConfigMap = true
			assert.False(t, *envFrom.ConfigMapRef.Optional, "configmap should not be optional")
		}
		if envFrom.SecretRef != nil && envFrom.SecretRef.Name == "test-secret" {
			foundCustomSecret = true
			assert.True(t, *envFrom.SecretRef.Optional, "custom secret should be optional")
		}
	}
	assert.True(t, foundConfigMap, "custom ConfigMapRef should be present")
	assert.True(t, foundCustomSecret, "custom SecretRef should be present")

	// Check base secret references
	assert.NotNil(t, container.EnvFrom[0].SecretRef)
	assert.NotNil(t, container.EnvFrom[1].SecretRef)
}

func readDefaultCR(name, namespace string) (*api.PerconaServerMongoDB, error) {
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "cr.yaml"))
	if err != nil {
		return nil, err
	}

	cr := &api.PerconaServerMongoDB{}

	if err := yaml.Unmarshal(data, cr); err != nil {
		return nil, err
	}

	cr.Name = name
	cr.Namespace = namespace
	cr.Spec.InitImage = "perconalab/percona-server-mongodb-operator:main"
	return cr, nil
}

func TestMongosServiceAnnotations(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		expose              api.MongosExpose
		svcName             string
		expectedAnnotations map[string]string
	}{
		"externalDNS single service: hostname without pod index": {
			expose: api.MongosExpose{
				Expose: api.Expose{
					ExposeType: corev1.ServiceTypeLoadBalancer,
					ExternalDNS: &api.ExternalDNSConfig{
						Prefix: "prod",
						Domain: "mongo.example.com",
						TTL:    120,
					},
				},
			},
			svcName: "test-cr-mongos",
			expectedAnnotations: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": "prod-mongos.mongo.example.com",
				"external-dns.alpha.kubernetes.io/ttl":      "120",
				"percona.com/external-dns-managed":          "true",
			},
		},
		"externalDNS servicePerPod: hostname with pod index": {
			expose: api.MongosExpose{
				ServicePerPod: true,
				Expose: api.Expose{
					ExposeType: corev1.ServiceTypeLoadBalancer,
					ExternalDNS: &api.ExternalDNSConfig{
						Prefix: "prod",
						Domain: "mongo.example.com",
					},
				},
			},
			svcName: "test-cr-mongos-2",
			expectedAnnotations: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": "prod-mongos-2.mongo.example.com",
				"percona.com/external-dns-managed":          "true",
			},
		},
		"no externalDNS: only user annotations": {
			expose: api.MongosExpose{
				Expose: api.Expose{
					ExposeType: corev1.ServiceTypeLoadBalancer,
					ServiceAnnotations: map[string]string{
						"percona.com/test": "annotation",
					},
				},
			},
			svcName: "test-cr-mongos",
			expectedAnnotations: map[string]string{
				"percona.com/test": "annotation",
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{
				Name:      "test-cr",
				Namespace: "test-ns",
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Sharding: api.Sharding{
						Mongos: &api.MongosSpec{
							Expose: tt.expose,
						},
					},
				},
			}
			svc := MongosService(cr, tt.svcName)
			assert.Equal(t, tt.expectedAnnotations, svc.Annotations)
		})
	}
}

func TestMongosLogCollector(t *testing.T) {
	t.Parallel()

	cr, err := readDefaultCR("test-cr", "test-ns")
	require.NoError(t, err)

	cr.Spec.LogCollector = &api.LogCollectorSpec{Enabled: true, Image: "log-test-image"}
	cr.Spec.Sharding.Mongos.Logs = &api.MongosLogsSpec{
		PersistentVolumeClaim: &api.PVCSpec{
			PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		},
	}
	require.NoError(t, cr.CheckNSetDefaults(t.Context(), version.PlatformKubernetes))

	templateSpec, err := MongosTemplateSpec(cr, cr.Spec.InitImage, logr.Discard(),
		StatefulConfigParams{}, []string{"cfg-0.test-cr-cfg.test-ns.svc.cluster.local:27017"}, true)
	require.NoError(t, err)

	spec := MongosStatefulsetSpec(cr, templateSpec)

	container := func(name string) *corev1.Container {
		for i, c := range spec.Template.Spec.Containers {
			if c.Name == name {
				return &spec.Template.Spec.Containers[i]
			}
		}
		return nil
	}

	// the collector sidecars run next to mongos
	for _, name := range []string{"logs", "logrotate"} {
		assert.NotNil(t, container(name), "container %s must be present", name)
	}

	// logs live on one PVC per pod, so the volume comes from the claim template
	// and must not be declared in the pod spec as well
	require.Len(t, spec.VolumeClaimTemplates, 1)
	assert.Equal(t, config.MongosLogVolClaimName, spec.VolumeClaimTemplates[0].Name)
	assert.Equal(t, resource.MustParse("1Gi"),
		spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage])
	for _, v := range spec.Template.Spec.Volumes {
		assert.NotEqual(t, config.MongosLogVolClaimName, v.Name,
			"the log volume must come from the claim template, not the pod spec")
	}

	// mongos and both sidecars have to agree on where the logs are
	for _, name := range []string{"mongos", "logs", "logrotate"} {
		c := container(name)
		require.NotNil(t, c)

		var mountPath string
		for _, m := range c.VolumeMounts {
			if m.Name == config.MongosLogVolClaimName {
				mountPath = m.MountPath
			}
		}
		assert.Equal(t, config.MongodContainerDataLogsDir, mountPath,
			"container %s must mount %s at the log dir", name, config.MongosLogVolClaimName)
	}

	// without this mongos logs to stdout only and there is nothing to collect
	assert.Contains(t, container("mongos").Env, corev1.EnvVar{
		Name:  "LOGCOLLECTOR_ENABLED",
		Value: "true",
	})
}

func TestMongosLogPVC(t *testing.T) {
	t.Parallel()

	mongosLabels := map[string]string{
		"app.kubernetes.io/component":  "mongos",
		"app.kubernetes.io/instance":   "test-cr",
		"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
		"app.kubernetes.io/name":       "percona-server-mongodb",
		"app.kubernetes.io/part-of":    "percona-server-mongodb",
	}

	tests := map[string]struct {
		storage             *api.PVCSpec
		expectedLabels      map[string]string
		expectedAnnotations map[string]string
		expectedSpec        corev1.PersistentVolumeClaimSpec
	}{
		"spec only": {
			storage: &api.PVCSpec{
				PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
					StorageClassName: new("standard"),
					AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			},
			expectedLabels:      mongosLabels,
			expectedAnnotations: nil,
			expectedSpec: corev1.PersistentVolumeClaimSpec{
				StorageClassName: new("standard"),
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		},
		"custom labels and annotations": {
			storage: &api.PVCSpec{
				Labels: map[string]string{
					"percona.com/test": "label",
				},
				Annotations: map[string]string{
					"percona.com/test": "annotation",
				},
				PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			},
			expectedLabels: map[string]string{
				"app.kubernetes.io/component":  "mongos",
				"app.kubernetes.io/instance":   "test-cr",
				"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
				"app.kubernetes.io/name":       "percona-server-mongodb",
				"app.kubernetes.io/part-of":    "percona-server-mongodb",
				"percona.com/test":             "label",
			},
			expectedAnnotations: map[string]string{
				"percona.com/test": "annotation",
			},
			expectedSpec: corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		},
		"custom labels do no win over operator labels": {
			storage: &api.PVCSpec{
				Labels: map[string]string{
					"app.kubernetes.io/part-of": "custom",
				},
				PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{},
			},
			expectedLabels: map[string]string{
				"app.kubernetes.io/component":  "mongos",
				"app.kubernetes.io/instance":   "test-cr",
				"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
				"app.kubernetes.io/name":       "percona-server-mongodb",
				"app.kubernetes.io/part-of":    "percona-server-mongodb",
			},
			expectedAnnotations: nil,
			expectedSpec:        corev1.PersistentVolumeClaimSpec{},
		},
		"no claim spec": {
			storage: &api.PVCSpec{
				Labels: map[string]string{
					"percona.com/test": "label",
				},
			},
			expectedLabels: map[string]string{
				"app.kubernetes.io/component":  "mongos",
				"app.kubernetes.io/instance":   "test-cr",
				"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
				"app.kubernetes.io/name":       "percona-server-mongodb",
				"app.kubernetes.io/part-of":    "percona-server-mongodb",
				"percona.com/test":             "label",
			},
			expectedAnnotations: nil,
			expectedSpec:        corev1.PersistentVolumeClaimSpec{},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{
				Name:      "test-cr",
				Namespace: "test-ns",
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Sharding: api.Sharding{
						Mongos: &api.MongosSpec{
							Logs: &api.MongosLogsSpec{
								PersistentVolumeClaim: tt.storage,
							},
						},
					},
				},
			}

			pvc := mongosLogPVC(cr)

			assert.Equal(t, config.MongosLogVolClaimName, pvc.Name)
			assert.Equal(t, "test-ns", pvc.Namespace)
			assert.Equal(t, tt.expectedLabels, pvc.Labels)
			assert.Equal(t, tt.expectedAnnotations, pvc.Annotations)
			assert.Equal(t, tt.expectedSpec, pvc.Spec)
		})
	}
}
