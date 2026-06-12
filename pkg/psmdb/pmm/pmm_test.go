package pmm

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestContainer(t *testing.T) {
	ctx := t.Context()

	tokenSecret := &corev1.Secret{
		Data: map[string][]byte{"PMM_SERVER_TOKEN": []byte(`token`)},
	}
	pmm2Secret := &corev1.Secret{
		Data: map[string][]byte{"PMM_SERVER_API_KEY": []byte(`key`)},
	}

	tests := map[string]struct {
		secret *corev1.Secret
		setup  func(cr *api.PerconaServerMongoDB)
		assert func(t *testing.T, container *corev1.Container)
	}{
		"pmm disabled": {
			setup:  func(cr *api.PerconaServerMongoDB) { cr.Spec.PMM.Enabled = false },
			assert: assertNilContainer,
		},
		"secret is nil": {
			assert: assertNilContainer,
		},
		"pmm enabled but secret token is empty": {
			secret: &corev1.Secret{Data: map[string][]byte{"PMM_SERVER_TOKEN": []byte(``)}},
			assert: assertNilContainer,
		},
		"pmm enabled but secret token is missing": {
			secret: &corev1.Secret{Data: map[string][]byte{"RANDOM_SECRET": []byte(`foo`)}},
			assert: assertNilContainer,
		},
		"pmm enabled - pmm3 container constructed": {
			secret: tokenSecret,
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.PMM.AuthenticationMechanism = "SCRAM-SHA-256"
			},
			assert: assertFullPMMContainer(buildExpectedPMMContainer()),
		},
		"pmm enabled - explicit SCRAM-SHA-256 honored on >=1.23.0": {
			secret: tokenSecret,
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.PMM.AuthenticationMechanism = "SCRAM-SHA-256"
			},
			assert: assertAuthMechanism("SCRAM-SHA-256"),
		},
		"pmm enabled - unset mechanism falls back to SCRAM-SHA-256 on >=1.23.0": {
			secret: tokenSecret,
			assert: assertAuthMechanism("SCRAM-SHA-256"),
		},
		"pmm enabled - explicit SCRAM-SHA-256 ignored on <1.23.0": {
			secret: tokenSecret,
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.CRVersion = "1.22.0"
				cr.Spec.PMM.AuthenticationMechanism = "SCRAM-SHA-256"
			},
			assert: assertAuthMechanism("SCRAM-SHA-1"),
		},
		"pmm2 enabled - explicit SCRAM-SHA-256 honored on >=1.23.0": {
			secret: pmm2Secret,
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.PMM.AuthenticationMechanism = "SCRAM-SHA-256"
			},
			assert: assertAuthMechanism("SCRAM-SHA-256"),
		},
		"pmm2 enabled - unset mechanism falls back to SCRAM-SHA-256 on >=1.23.0": {
			secret: pmm2Secret,
			assert: assertAuthMechanism("SCRAM-SHA-256"),
		},
		"pmm enabled - TLS disabled omits authentication-mechanism flag": {
			secret: tokenSecret,
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.TLS = &api.TLSSpec{Mode: api.TLSModeDisabled}
				cr.Spec.PMM.AuthenticationMechanism = "SCRAM-SHA-256"
			},
			assert: assertNoAuthMechanism(),
		},
		"pmm enabled - query-source=mongolog is set": {
			secret: tokenSecret,
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.PMM.QuerySource = "mongolog"
			},
			assert: assertQuerySource("mongolog"),
		},
		"pmm enabled - query-source=profiler is set": {
			secret: tokenSecret,
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.PMM.QuerySource = "profiler"
			},
			assert: assertQuerySource("profiler"),
		},
		"pmm enabled - query-source not set omits flag": {
			secret: tokenSecret,
			assert: assertNoQuerySource(),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := defaultPMMCR()
			if tt.setup != nil {
				tt.setup(cr)
			}
			container := Container(ctx, cr, tt.secret, 27017, cr.Spec.PMM.MongodParams)
			tt.assert(t, container)
		})
	}
}

func defaultPMMCR() *api.PerconaServerMongoDB {
	boolTrue := true
	return &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cr",
			Namespace: "test-ns",
		},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion:       version.Version(),
			ImagePullPolicy: corev1.PullAlways,
			PMM: api.PMMSpec{
				Enabled:           true,
				Image:             "pmm-image",
				ServerHost:        "server-host",
				CustomClusterName: "custom-cluster",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceCPU: resource.MustParse("100m"),
					},
				},
				MongodParams: "-param custom-mongodb-param",
				ContainerSecurityContext: &corev1.SecurityContext{
					RunAsNonRoot: &boolTrue,
				},
			},
		},
	}
}

func assertNilContainer(t *testing.T, container *corev1.Container) {
	assert.Nil(t, container)
}

func assertAuthMechanism(want string) func(t *testing.T, container *corev1.Container) {
	return func(t *testing.T, container *corev1.Container) {
		if !assert.NotNil(t, container) {
			return
		}
		var prerun string
		for _, ev := range container.Env {
			if ev.Name == "PMM_AGENT_PRERUN_SCRIPT" {
				prerun = ev.Value
				break
			}
		}
		assert.Contains(t, prerun, "--authentication-mechanism="+want)
	}
}

func assertQuerySource(want string) func(t *testing.T, container *corev1.Container) {
	return func(t *testing.T, container *corev1.Container) {
		if !assert.NotNil(t, container) {
			return
		}
		var prerun string
		for _, ev := range container.Env {
			if ev.Name == "PMM_AGENT_PRERUN_SCRIPT" {
				prerun = ev.Value
				break
			}
		}
		assert.Contains(t, prerun, "--query-source="+want)
	}
}

func assertNoQuerySource() func(t *testing.T, container *corev1.Container) {
	return func(t *testing.T, container *corev1.Container) {
		if !assert.NotNil(t, container) {
			return
		}
		var prerun string
		for _, ev := range container.Env {
			if ev.Name == "PMM_AGENT_PRERUN_SCRIPT" {
				prerun = ev.Value
				break
			}
		}
		assert.NotContains(t, prerun, "--query-source")
	}
}

func assertNoAuthMechanism() func(t *testing.T, container *corev1.Container) {
	return func(t *testing.T, container *corev1.Container) {
		if !assert.NotNil(t, container) {
			return
		}
		var prerun string
		for _, ev := range container.Env {
			if ev.Name == "PMM_AGENT_PRERUN_SCRIPT" {
				prerun = ev.Value
				break
			}
		}
		assert.NotContains(t, prerun, "--authentication-mechanism")
		assert.NotContains(t, prerun, "--tls")
	}
}

func assertFullPMMContainer(expected *corev1.Container) func(t *testing.T, container *corev1.Container) {
	return func(t *testing.T, container *corev1.Container) {
		if !assert.NotNil(t, container) {
			return
		}
		assert.Equal(t, expected.Name, container.Name)
		assert.Equal(t, expected.Image, container.Image)
		assert.Equal(t, len(expected.Env), len(container.Env))
		for index, ev := range container.Env {
			assert.Equal(t, expected.Env[index].Name, ev.Name)
			assert.Equal(t, expected.Env[index].Value, ev.Value)
		}
		for i, port := range expected.Ports {
			assert.Equal(t, expected.Ports[i].Name, port.Name)
		}
		assert.Equal(t, expected.Resources, container.Resources)
		assert.Equal(t, expected.ImagePullPolicy, container.ImagePullPolicy)
		assert.Equal(t, expected.SecurityContext, container.SecurityContext)
		assert.Equal(t, len(expected.VolumeMounts), len(container.VolumeMounts))
		for i, volumeMount := range container.VolumeMounts {
			assert.Equal(t, expected.VolumeMounts[i].Name, volumeMount.Name)
			assert.Equal(t, expected.VolumeMounts[i].MountPath, volumeMount.MountPath)
			assert.Equal(t, expected.VolumeMounts[i].ReadOnly, volumeMount.ReadOnly)
		}
	}
}

func buildExpectedPMMContainer() *corev1.Container {
	const (
		name         = "pmm-client"
		portStart    = 30100
		portEnd      = 30105
		listenPort   = 7777
		configFile   = "/usr/local/percona/pmm/config/pmm-agent.yaml"
		tempDir      = "/tmp/pmm"
		prerunScript = `cat /etc/mongodb-ssl/tls.key /etc/mongodb-ssl/tls.crt > /tmp/tls.pem;
pmm-admin status --wait=10s;
pmm-admin add $(DB_TYPE) $(PMM_ADMIN_CUSTOM_PARAMS) --skip-connection-check --metrics-mode=push  --username=$(DB_USER) --password=$(DB_PASSWORD) --cluster=$(CLUSTER_NAME) --service-name=$(PMM_AGENT_SETUP_NODE_NAME) --host=$(DB_HOST) --port=$(DB_PORT) --tls --tls-skip-verify --tls-certificate-key-file=/tmp/tls.pem --tls-ca-file=/etc/mongodb-ssl/ca.crt --authentication-mechanism=SCRAM-SHA-256 --authentication-database=admin;
pmm-admin annotate --service-name=$(PMM_AGENT_SETUP_NODE_NAME) 'Service restarted'`
	)

	var ports []corev1.ContainerPort
	ports = append(ports, corev1.ContainerPort{ContainerPort: int32(listenPort)})
	for p := portStart; p <= portEnd; p++ {
		ports = append(ports, corev1.ContainerPort{ContainerPort: int32(p)})
	}

	envVars := []corev1.EnvVar{
		{Name: "DB_TYPE", Value: "mongodb"},
		{Name: "DB_USER", ValueFrom: &corev1.EnvVarSource{}},
		{Name: "DB_PASSWORD", ValueFrom: &corev1.EnvVarSource{}},
		{Name: "DB_HOST", Value: "localhost"},
		{Name: "DB_CLUSTER", Value: "test-cr"},
		{Name: "DB_PORT", Value: "27017"},
		{Name: "CLUSTER_NAME", Value: "custom-cluster"},
		{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{}},
		{Name: "POD_NAMESPACE", ValueFrom: &corev1.EnvVarSource{}},
		{Name: "PMM_AGENT_SERVER_ADDRESS", Value: "server-host"},
		{Name: "PMM_AGENT_SERVER_USERNAME", Value: "service_token"},
		{Name: "PMM_AGENT_SERVER_PASSWORD", ValueFrom: &corev1.EnvVarSource{}},
		{Name: "PMM_AGENT_LISTEN_PORT", Value: strconv.Itoa(listenPort)},
		{Name: "PMM_AGENT_PORTS_MIN", Value: strconv.Itoa(portStart)},
		{Name: "PMM_AGENT_PORTS_MAX", Value: strconv.Itoa(portEnd)},
		{Name: "PMM_AGENT_CONFIG_FILE", Value: configFile},
		{Name: "PMM_AGENT_SERVER_INSECURE_TLS", Value: "1"},
		{Name: "PMM_AGENT_LISTEN_ADDRESS", Value: "0.0.0.0"},
		{Name: "PMM_AGENT_SETUP_NODE_NAME", Value: "$(POD_NAMESPACE)-$(POD_NAME)"},
		{Name: "PMM_AGENT_SETUP", Value: "1"},
		{Name: "PMM_AGENT_SETUP_FORCE", Value: "1"},
		{Name: "PMM_AGENT_SETUP_NODE_TYPE", Value: "container"},
		{Name: "PMM_AGENT_SETUP_METRICS_MODE", Value: "push"},
		{Name: "PMM_ADMIN_CUSTOM_PARAMS", Value: "-param custom-mongodb-param"},
		{Name: "PMM_AGENT_SIDECAR", Value: "true"},
		{Name: "PMM_AGENT_SIDECAR_SLEEP", Value: "5"},
		{Name: "PMM_AGENT_PATHS_TEMPDIR", Value: tempDir},
		{Name: "PMM_AGENT_PRERUN_SCRIPT", Value: prerunScript},
	}

	boolTrue := true

	return &corev1.Container{
		Name:            name,
		Image:           "pmm-image",
		Ports:           ports,
		ImagePullPolicy: corev1.PullAlways,
		Env:             envVars,
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("100m"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "ssl",
				MountPath: config.SSLDir,
				ReadOnly:  true,
			},
			{
				Name:      "mongod-data",
				MountPath: config.MongodContainerDataDir,
				ReadOnly:  true,
			},
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot: &boolTrue,
		},
	}
}
