package perconaservermongodb

import (
	"context"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/yaml"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/logcollector"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/logcollector/logrotate"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestReconcileStatefulSet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const (
		ns     = "reconcile-statefulset"
		crName = ns + "-cr"
	)

	defaultCR, err := readDefaultCR(crName, ns)
	if err != nil {
		t.Fatal(err)
	}

	defaultCR.Spec.Replsets[0].NonVoting.Enabled = true
	defaultCR.Spec.Replsets[0].Hidden.Enabled = true
	defaultCR.Spec.LogCollector.Configuration = "config"
	for _, rs := range []*api.ReplsetSpec{defaultCR.Spec.Replsets[0], defaultCR.Spec.Sharding.ConfigsvrReplSet} {
		rs.LogStorage = logStorageSpec("2Gi")
		rs.NonVoting.LogStorage = logStorageSpec("2Gi")
		rs.Hidden.LogStorage = logStorageSpec("2Gi")
	}
	if err := defaultCR.CheckNSetDefaults(ctx, version.PlatformKubernetes); err != nil {
		t.Fatal(err)
	}

	caCert, tlsCert, tlsKey, err := tls.Issue(tls.GetCertificateSans(defaultCR))
	if err != nil {
		t.Fatal(err)
	}

	defaultCR.Spec.Replsets[0].Env = []corev1.EnvVar{
		{Name: "TEST_ENV1", Value: "test-value1"},
		{Name: "TEST_ENV2", Value: "test-value2"},
	}
	defaultCR.Spec.Replsets[0].EnvFrom = []corev1.EnvFromSource{
		{
			ConfigMapRef: &corev1.ConfigMapEnvSource{
				Name:     "test-configmap",
				Optional: new(true),
			},
		},
	}
	defaultCR.Spec.Sharding.ConfigsvrReplSet.Env = []corev1.EnvVar{
		{Name: "CFG_TEST_ENV1", Value: "cfg-test-value1"},
		{Name: "CFG_TEST_ENV2", Value: "cfg-test-value2"},
	}
	defaultCR.Spec.Sharding.ConfigsvrReplSet.EnvFrom = []corev1.EnvFromSource{
		{
			ConfigMapRef: &corev1.ConfigMapEnvSource{
				Name:     "test-configmap-cfg",
				Optional: new(true),
			},
		},
	}

	tests := []struct {
		name           string
		cr             *api.PerconaServerMongoDB
		rsName         string
		component      string
		ls             map[string]string
		crUpdate       func(cr *api.PerconaServerMongoDB)
		additionalObjs []client.Object

		compareFile string
	}{
		{
			name:        "rs0-mongod",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			component:   naming.ComponentMongod,
			compareFile: "reconcile-statefulset/rs0-mongod.yaml",
		},
		{
			name:        "rs0-arbiter",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			component:   naming.ComponentArbiter,
			compareFile: "reconcile-statefulset/rs0-arbiter.yaml",
		},
		{
			name:        "rs0-non-voting",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			component:   naming.ComponentNonVoting,
			compareFile: "reconcile-statefulset/rs0-nv.yaml",
		},
		{
			name:        "rs0-hidden",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			component:   naming.ComponentHidden,
			compareFile: "reconcile-statefulset/rs0-hidden.yaml",
		},
		{
			name:        "cfg-mongod",
			cr:          defaultCR.DeepCopy(),
			rsName:      "cfg",
			component:   naming.ComponentMongod,
			compareFile: "reconcile-statefulset/cfg-mongod.yaml",
		},
		{
			name:        "cfg-arbiter",
			cr:          defaultCR.DeepCopy(),
			rsName:      "cfg",
			component:   naming.ComponentArbiter,
			compareFile: "reconcile-statefulset/cfg-arbiter.yaml",
		},
		{
			name:        "cfg-non-voting",
			cr:          defaultCR.DeepCopy(),
			rsName:      "cfg",
			component:   naming.ComponentNonVoting,
			compareFile: "reconcile-statefulset/cfg-nv.yaml",
		},
		{
			name:        "cfg-hidden",
			cr:          defaultCR.DeepCopy(),
			rsName:      "cfg",
			component:   naming.ComponentHidden,
			compareFile: "reconcile-statefulset/cfg-hidden.yaml",
		},
		{
			name:        "rs0-no-log-storage",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			component:   naming.ComponentMongod,
			compareFile: "reconcile-statefulset/rs0-no-log-storage.yaml",
			crUpdate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].LogStorage = nil
			},
		},
		{
			name:        "rs0-journal",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			component:   naming.ComponentMongod,
			compareFile: "reconcile-statefulset/rs0-journal.yaml",
			crUpdate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].JournalStorage = &api.StorageSpec{
					PersistentVolumeClaim: &api.PVCSpec{
						PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
							AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
							Resources: corev1.VolumeResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceStorage: resource.MustParse("5Gi"),
								},
							},
						},
					},
				}
			},
		},
		{
			name:        "rs0-logrotate",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			component:   naming.ComponentMongod,
			compareFile: "reconcile-statefulset/rs0-logrotate.yaml",
			crUpdate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.LogCollector.LogRotate = &api.LogRotateSpec{
					Configuration: "test-config",
					ExtraConfig: corev1.LocalObjectReference{
						Name: "extra-config",
					},
					Schedule: "0 0 */2 * *",
				}
			},
			additionalObjs: []client.Object{
				&corev1.ConfigMap{
					Name:      logrotate.ConfigMapName(crName),
					Namespace: ns,
					Data: map[string]string{
						logrotate.MongodbConfig: "custom-config",
					},
				},
				&corev1.ConfigMap{
					Name:      "extra-config",
					Namespace: ns,
					Data: map[string]string{
						"custom.conf": "custom-config",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			mockObjs := []client.Object{
				tt.cr,
				&corev1.Secret{
					Name:      crName + "-ssl",
					Namespace: tt.cr.Namespace,
					Data: map[string][]byte{
						"ca.crt":  caCert,
						"tls.crt": tlsCert,
						"tls.key": tlsKey,
					},
				},
				&corev1.Secret{
					Name:      crName + "-ssl-internal",
					Namespace: tt.cr.Namespace,
				},
				&corev1.Secret{
					Name:      tls.CertificatePBMClient(tt.cr).SecretName(),
					Namespace: tt.cr.Namespace,
				},

				&corev1.ConfigMap{
					Name:      logcollector.ConfigMapName(tt.cr.Name),
					Namespace: tt.cr.Namespace,
					Data: map[string]string{
						"fluentbit_custom.conf": "config",
					},
				},
			}

			mockObjs = append(mockObjs, tt.additionalObjs...)
			r := buildFakeClient(mockObjs...)

			if tt.crUpdate != nil {
				tt.crUpdate(tt.cr)
			}

			rs := tt.cr.Spec.Replset(tt.rsName)

			var ls map[string]string
			switch tt.component {
			case naming.ComponentMongod:
				ls = naming.MongodLabels(tt.cr, rs)
			case naming.ComponentArbiter:
				ls = naming.ArbiterLabels(tt.cr, rs)
			case naming.ComponentNonVoting:
				ls = naming.NonVotingLabels(tt.cr, rs)
			case naming.ComponentHidden:
				ls = naming.HiddenLabels(tt.cr, rs)
			default:
				t.Fatalf("unexpected component: %s", tt.component)
			}

			sts, err := r.reconcileStatefulSet(ctx, tt.cr, rs, ls)
			if err != nil {
				t.Fatalf("reconcileStatefulSet() error = %v", err)
			}

			// Since version v0.22.0 of the runtime controller, it does not return the GVK for a type.
			// Github issue: https://github.com/kubernetes-sigs/controller-runtime/issues/3302
			gvk, err := apiutil.GVKForObject(sts, scheme.Scheme)
			require.NoError(t, err)
			require.False(t, gvk.Empty())

			sts.Kind = gvk.Kind
			sts.APIVersion = gvk.GroupVersion().String()

			if os.Getenv("UPDATE_COMPARE_FILES") == "1" {
				updateSts(t, tt.compareFile, sts)
			}

			compareSts(t, sts, expectedSts(t, tt.compareFile))
		})
	}
}

func updateSts(t *testing.T, filename string, sts *appsv1.StatefulSet) {
	t.Helper()

	sts = sts.DeepCopy()
	delete(sts.Annotations, "percona.com/last-config-hash")
	delete(sts.Spec.Template.Annotations, naming.AnnotationSSLHash)
	delete(sts.Spec.Template.Annotations, naming.AnnotationSSLInternalHash)

	data, err := yaml.Marshal(sts)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile("testdata/"+filename, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func expectedSts(t *testing.T, filename string) *appsv1.StatefulSet {
	t.Helper()

	data, err := os.ReadFile("testdata/" + filename)
	if err != nil {
		t.Fatal(err)
	}
	sts := new(appsv1.StatefulSet)

	if err := yaml.Unmarshal(data, sts); err != nil {
		t.Fatal(err)
	}

	return sts
}

func compareSts(t *testing.T, got, want *appsv1.StatefulSet) {
	t.Helper()

	if !reflect.DeepEqual(got.TypeMeta, want.TypeMeta) {
		t.Fatal(cmp.Diff(want.TypeMeta, got.TypeMeta))
	}
	compareObjectMeta := func(got, want metav1.ObjectMeta) {
		delete(got.Annotations, "percona.com/last-config-hash")
		gotBytes, err := yaml.Marshal(got)
		if err != nil {
			t.Fatalf("error marshaling got: %v", err)
		}
		wantBytes, err := yaml.Marshal(want)
		if err != nil {
			t.Fatalf("error marshaling want: %v", err)
		}
		if string(gotBytes) != string(wantBytes) {
			t.Fatal(cmp.Diff(string(wantBytes), string(gotBytes)))
		}
	}
	compareObjectMeta(got.ObjectMeta, want.ObjectMeta)

	compareSpec := func(got, want appsv1.StatefulSetSpec) {
		delete(got.Template.Annotations, naming.AnnotationSSLHash)
		delete(got.Template.Annotations, naming.AnnotationSSLInternalHash)
		delete(got.Template.Annotations, naming.AnnotationPBMTLSHash)
		gotBytes, err := yaml.Marshal(got)
		if err != nil {
			t.Fatalf("error marshaling got: %v", err)
		}
		wantBytes, err := yaml.Marshal(want)
		if err != nil {
			t.Fatalf("error marshaling want: %v", err)
		}
		if string(gotBytes) != string(wantBytes) {
			t.Fatal(cmp.Diff(string(wantBytes), string(gotBytes)))
		}
	}
	compareSpec(got.Spec, want.Spec)

	if !reflect.DeepEqual(got.Status, want.Status) {
		t.Fatal(cmp.Diff(want.Status, got.Status))
	}
}

func TestGetStatefulsetFromReplsetConfigNotInCache(t *testing.T) {
	ctx := context.Background()

	const (
		ns     = "sts-config-not-in-cache"
		crName = ns + "-cr"
	)

	cr, err := readDefaultCR(crName, ns)
	require.NoError(t, err)
	cr.Spec.Replsets[0].Configuration = "operationProfiling:\n  mode: slowOp\n"
	require.NoError(t, cr.CheckNSetDefaults(ctx, version.PlatformKubernetes))

	rs := cr.Spec.Replset("rs0")
	configMapName := naming.MongodCustomConfigName(cr, rs)
	objs := []client.Object{
		cr,
		&corev1.Secret{
			Name:      crName + "-ssl",
			Namespace: ns,
		},
		&corev1.Secret{
			Name:      crName + "-ssl-internal",
			Namespace: ns,
		},
		&corev1.Secret{
			Name:      tls.CertificatePBMClient(cr).SecretName(),
			Namespace: ns,
		},
		&corev1.ConfigMap{
			Name:      configMapName,
			Namespace: ns,
			Data: map[string]string{
				"mongod.conf": string(rs.Configuration),
			},
		},
	}

	want, err := buildFakeClient(objs...).getStatefulsetFromReplset(ctx, cr, rs, naming.MongodLabels(cr, rs))
	require.NoError(t, err)

	// staleCache hides the config map from the first `misses` reads
	staleCache := func(misses int) *ReconcilePerconaServerMongoDB {
		r := buildFakeClient(objs...)
		r.client = interceptor.NewClient(r.client.(client.WithWatch), interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.ConfigMap); ok && key.Name == configMapName && misses > 0 {
					misses--
					return k8serrors.NewNotFound(corev1.Resource("configmaps"), key.Name)
				}
				return c.Get(ctx, key, obj, opts...)
			},
		})
		return r
	}

	t.Run("config map appears in cache", func(t *testing.T) {
		got, err := staleCache(2).getStatefulsetFromReplset(ctx, cr, rs, naming.MongodLabels(cr, rs))
		require.NoError(t, err)
		require.NotEmpty(t, got.Spec.Template.Annotations[naming.AnnotationConfigHash])
		require.Equal(t, want.Spec, got.Spec)
	})

	t.Run("config map never appears in cache", func(t *testing.T) {
		_, err := staleCache(100).getStatefulsetFromReplset(ctx, cr, rs, naming.MongodLabels(cr, rs))
		require.ErrorIs(t, err, errConfigNotInCache)
	})
}

func TestReconcileStatefulSetFinishedResize(t *testing.T) {
	ctx := t.Context()

	const (
		ns     = "sts-finished-resize"
		crName = ns + "-cr"
	)

	cr, err := readDefaultCR(crName, ns)
	require.NoError(t, err)
	require.NoError(t, cr.CheckNSetDefaults(ctx, version.PlatformKubernetes))

	rs := cr.Spec.Replset("rs0")
	ls := naming.MongodLabels(cr, rs)
	objs := []client.Object{
		cr,
		&corev1.Secret{Name: crName + "-ssl", Namespace: ns},
		&corev1.Secret{Name: crName + "-ssl-internal", Namespace: ns},
		&corev1.Secret{Name: tls.CertificatePBMClient(cr).SecretName(), Namespace: ns},
	}

	sts, err := buildFakeClient(objs...).getStatefulsetFromReplset(ctx, cr, rs, ls)
	require.NoError(t, err)
	sts.Annotations = map[string]string{
		api.AnnotationPVCResizeInProgress: time.Now().Format(time.RFC3339),
		api.AnnotationPVCResizeClaim:      config.MongodDataVolClaimName,
	}

	pod := newPod(ns, sts.Name+"-0", ls)
	size := rs.VolumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
	pvc := newPVC(ns, config.MongodDataVolClaimName+"-"+pod.Name, ls, size, size)

	r := buildFakeClient(append(objs, sts, pod, pvc)...)

	_, err = r.reconcileStatefulSet(ctx, cr, rs, ls)
	require.NoError(t, err)

	err = r.client.Get(ctx, client.ObjectKeyFromObject(sts), new(appsv1.StatefulSet))
	require.True(t, k8serrors.IsNotFound(err), "statefulset must be rebuilt on the next reconcile, got %v", err)
}

func TestStartupProbeDefaults(t *testing.T) {
	startupCommand := []string{"/opt/percona/mongodb-healthcheck", "k8s", "startup", "--component", "mongod"}
	tlsStartupCommand := append(slices.Clone(startupCommand),
		"--ssl", "--sslInsecure",
		"--sslCAFile", "/etc/mongodb-ssl/ca.crt",
		"--sslPEMKeyFile", "/tmp/tls.pem")

	tests := map[string]struct {
		modify          func(cr *api.PerconaServerMongoDB)
		wantNil         bool
		wantRS          int32
		wantNonVoting   int32
		wantHidden      int32
		wantRSPeriod    int32
		wantCfgFailures int32
		wantCommand     []string
		wantExec        []string
	}{
		"not set before 1.24.0": {
			modify:  func(cr *api.PerconaServerMongoDB) { cr.Spec.CRVersion = "1.23.0" },
			wantNil: true,
		},
		"defaults to 24h": {
			modify:          func(cr *api.PerconaServerMongoDB) {},
			wantRS:          8640,
			wantNonVoting:   8640,
			wantHidden:      8640,
			wantRSPeriod:    10,
			wantCfgFailures: 8640,
		},
		"replset override is inherited by nonvoting and hidden": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].StartupProbe = &corev1.Probe{FailureThreshold: 100}
			},
			wantRS:          100,
			wantNonVoting:   100,
			wantHidden:      100,
			wantRSPeriod:    10,
			wantCfgFailures: 8640,
		},
		"nonvoting override doesn't change replset": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].NonVoting.StartupProbe = &corev1.Probe{FailureThreshold: 50, PeriodSeconds: 30}
			},
			wantRS:          8640,
			wantNonVoting:   50,
			wantHidden:      8640,
			wantRSPeriod:    10,
			wantCfgFailures: 8640,
		},
		"no TLS flags when TLS is disabled": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.TLS = &api.TLSSpec{Mode: api.TLSModeDisabled}
				cr.Spec.Unsafe.TLS = true
			},
			wantRS:          8640,
			wantNonVoting:   8640,
			wantHidden:      8640,
			wantRSPeriod:    10,
			wantCfgFailures: 8640,
			wantCommand:     startupCommand,
		},
		"user handler is kept": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].StartupProbe = &corev1.Probe{
					Exec: &corev1.ExecAction{Command: []string{"/bin/true"}},
				}
			},
			wantRS:          8640,
			wantNonVoting:   8640,
			wantHidden:      8640,
			wantRSPeriod:    10,
			wantCfgFailures: 8640,
			wantExec:        []string{"/bin/true"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr, err := readDefaultCR("startup-probe", "startup-probe")
			require.NoError(t, err)
			cr.Spec.Replsets[0].NonVoting.Enabled = true
			cr.Spec.Replsets[0].Hidden.Enabled = true
			tt.modify(cr)
			require.NoError(t, cr.CheckNSetDefaults(t.Context(), version.PlatformKubernetes))

			rs := cr.Spec.Replsets[0]
			cfg := cr.Spec.Sharding.ConfigsvrReplSet
			if tt.wantNil {
				require.Nil(t, rs.StartupProbe)
				require.Nil(t, rs.NonVoting.StartupProbe)
				require.Nil(t, rs.Hidden.StartupProbe)
				require.Nil(t, cfg.StartupProbe)
				return
			}

			require.Equal(t, tt.wantRS, rs.StartupProbe.FailureThreshold)
			require.Equal(t, tt.wantRSPeriod, rs.StartupProbe.PeriodSeconds)
			require.Equal(t, tt.wantNonVoting, rs.NonVoting.StartupProbe.FailureThreshold)
			require.Equal(t, tt.wantHidden, rs.Hidden.StartupProbe.FailureThreshold)
			require.Equal(t, tt.wantCfgFailures, cfg.StartupProbe.FailureThreshold)
			wantCommand := tlsStartupCommand
			if tt.wantCommand != nil {
				wantCommand = tt.wantCommand
			}
			for _, p := range []*corev1.Probe{rs.StartupProbe, rs.NonVoting.StartupProbe, rs.Hidden.StartupProbe} {
				if tt.wantExec != nil {
					require.Equal(t, tt.wantExec, p.Exec.Command)
					continue
				}
				require.Equal(t, wantCommand, p.Exec.Command)
			}
			require.Equal(t, wantCommand, cfg.StartupProbe.Exec.Command)
		})
	}
}

func TestReconcileStatefulSetMongodConfigSource(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		configMap  bool
		secret     bool
		wantSecret bool
	}{
		"configmap":            {configMap: true},
		"secret":               {secret: true, wantSecret: true},
		"secret and configmap": {configMap: true, secret: true, wantSecret: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr, err := readDefaultCR("config-source", "config-source")
			require.NoError(t, err)
			cr.Spec.Backup.Enabled = false
			require.NoError(t, cr.CheckNSetDefaults(t.Context(), version.PlatformKubernetes))
			rs := cr.Spec.Replsets[0]
			configName := naming.MongodCustomConfigName(cr, rs)

			objs := []client.Object{
				cr,
				&corev1.Secret{Name: api.SSLSecretName(cr), Namespace: cr.Namespace},
				&corev1.Secret{Name: api.SSLInternalSecretName(cr), Namespace: cr.Namespace},
			}
			if tt.configMap {
				objs = append(objs, &corev1.ConfigMap{Name: configName, Namespace: cr.Namespace, Data: map[string]string{"mongod.conf": "{}"}})
			}
			if tt.secret {
				objs = append(objs, &corev1.Secret{Name: configName, Namespace: cr.Namespace, Data: map[string][]byte{"mongod.conf": []byte("{}")}})
			}
			r := buildFakeClient(objs...)

			sts, err := r.reconcileStatefulSet(t.Context(), cr, rs, naming.MongodLabels(cr, rs))
			require.NoError(t, err)

			i := slices.IndexFunc(sts.Spec.Template.Spec.Volumes, func(v corev1.Volume) bool { return v.Name == "config" })
			require.NotEqual(t, -1, i)
			vol := sts.Spec.Template.Spec.Volumes[i]
			if tt.wantSecret {
				require.NotNil(t, vol.Secret)
				assert.Equal(t, configName, vol.Secret.SecretName)
				assert.Nil(t, vol.ConfigMap)
			} else {
				require.NotNil(t, vol.ConfigMap)
				assert.Equal(t, configName, vol.ConfigMap.Name)
				assert.Nil(t, vol.Secret)
			}
			assert.Contains(t, sts.Spec.Template.Spec.Containers[0].Args, "--config=/etc/mongodb-config/mongod.conf")
		})
	}
}
