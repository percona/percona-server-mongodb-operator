package psmdb

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/ptr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestMongosService(t *testing.T) {
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
						AppProtocol: ptr.To("mongo"),
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
						AppProtocol: ptr.To("mongo"),
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
				LoadBalancerClass:        ptr.To("eks.amazonaws.com/nlb"),
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
						AppProtocol: ptr.To("mongo"),
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
				LoadBalancerClass:        ptr.To("eks.amazonaws.com/nlb"),
				LoadBalancerSourceRanges: []string{"10.0.0.0/16"},
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cr",
					Namespace: "test-ns",
				},
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
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "test-configmap",
				},
				Optional: ptr.To(false),
			},
		},
		{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "test-secret",
				},
				Optional: ptr.To(true),
			},
		},
	}

	container, err := mongosContainer(cr, false, []string{"cfg-0.test-cr-cfg.test-ns.svc.cluster.local:27017"})
	assert.NoError(t, err)

	// Basic container fields
	assert.Equal(t, "mongos", container.Name)
	assert.Equal(t, "percona/percona-server-mongodb:8.0.19-7", container.Image)
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
