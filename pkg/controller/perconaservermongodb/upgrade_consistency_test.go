package perconaservermongodb

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/yaml"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

// TestUpgradeConsistency renders the objects of a cluster created at the oldest supported crVersion
// and upgraded through the later ones, and compares them with testdata/upgrade-consistency/<name>.yaml.
// Run with UPDATE_COMPARE_FILES=1 to regenerate the files; `make update-upgrade-consistency-test`
// rotates the versions.
func TestUpgradeConsistency(t *testing.T) {
	t.Parallel()

	// the keyfile secret created at the oldest crVersion stays after upgrades
	keyfile := []client.Object{&corev1.Secret{Name: "some-name-mongodb-keyfile", Namespace: "upgrade"}}

	tests := []struct {
		name        string
		cr          string
		crVersion   string
		certManager bool
		objects     []client.Object
	}{
		{name: "1220", cr: "cr.yml", crVersion: "1.22.0"},
		{name: "1231", cr: "cr.yml", crVersion: "1.23.1", objects: keyfile},
		{name: "1240", cr: "cr.yml", crVersion: "1.24.0", objects: keyfile},
		{name: "sharded-tls-1220", cr: "cr-sharded-tls.yml", crVersion: "1.22.0", certManager: true},
		{name: "sharded-tls-1231", cr: "cr-sharded-tls.yml", crVersion: "1.23.1", certManager: true, objects: keyfile},
		{name: "sharded-tls-1240", cr: "cr-sharded-tls.yml", crVersion: "1.24.0", certManager: true, objects: keyfile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := renderUpgradedCluster(t, tt.cr, tt.crVersion, tt.certManager, tt.objects)
			file := filepath.Join("testdata", "upgrade-consistency", tt.name+".yaml")

			if os.Getenv("UPDATE_COMPARE_FILES") == "1" {
				require.NoError(t, os.WriteFile(file, got, 0o644))
			}

			want, err := os.ReadFile(file)
			require.NoError(t, err)
			if diff := cmp.Diff(string(want), string(got)); diff != "" {
				t.Fatalf("rendered objects differ from %s (-want +got):\n%s", file, diff)
			}
		})
	}
}

func renderUpgradedCluster(t *testing.T, crFile, crVersion string, certManager bool, seed []client.Object) []byte {
	t.Helper()

	ctx := t.Context()
	const ns = "upgrade"

	data, err := os.ReadFile(filepath.Join("testdata", "upgrade-consistency", crFile))
	require.NoError(t, err)
	cr := new(api.PerconaServerMongoDB)
	require.NoError(t, yaml.Unmarshal(data, cr))
	cr.Namespace = ns
	cr.Spec.CRVersion = crVersion
	cr.Spec.Image = "percona/percona-server-mongodb:sometag"
	cr.Spec.InitImage = "percona/percona-server-mongodb-operator:sometag"
	cr.Spec.Backup.Image = "percona/percona-backup-mongodb:sometag"
	cr.Spec.UpgradeOptions.Apply = "Never"
	require.NoError(t, cr.CheckNSetDefaults(ctx, version.PlatformKubernetes))

	repls := slices.Clone(cr.Spec.Replsets)
	if cr.Spec.Sharding.Enabled {
		repls = append([]*api.ReplsetSpec{cr.Spec.Sharding.ConfigsvrReplSet}, repls...)
	}

	objs := []client.Object{cr}
	for _, rs := range repls {
		objs = append(objs, fakePodsForRS(cr, rs)...)
	}
	for _, obj := range seed {
		objs = append(objs, obj.DeepCopyObject().(client.Object))
	}
	r := buildFakeClient(objs...)

	require.NoError(t, r.reconcileUsersSecret(ctx, cr))
	require.NoError(t, r.reconcileMongodConfigMaps(ctx, cr, repls))
	require.NoError(t, r.reconcileMongosConfigMaps(ctx, cr))
	require.NoError(t, r.reconcileLogCollectorConfigMaps(ctx, cr))
	require.NoError(t, r.reconcileLogRotateConfigMaps(ctx, cr))

	if certManager {
		_, err := r.applyCertManagerCertificates(ctx, cr, noWaitCertManager{tls.NewCertManagerController(r.client, r.scheme, false)})
		require.NoError(t, err)
		// stand in for the secrets cert-manager issues
		for _, cert := range []tls.Certificate{tls.CertificateTLS(cr, false), tls.CertificateTLS(cr, true)} {
			err := r.client.Create(ctx, &corev1.Secret{
				Name:      cert.SecretName(),
				Namespace: ns,
				Data:      map[string][]byte{"ca.crt": nil, "tls.crt": nil, "tls.key": nil},
			})
			require.NoError(t, client.IgnoreAlreadyExists(err))
		}
	} else {
		require.NoError(t, r.reconcileSSL(ctx, cr))
	}
	require.NoError(t, r.ensureSecurityKeys(ctx, cr))

	for _, rs := range repls {
		require.NoError(t, r.reconcileReplset(ctx, cr, rs))
	}
	require.NoError(t, r.reconcileServices(ctx, cr, repls))
	require.NoError(t, r.reconcileMongosStatefulset(ctx, cr))

	var out []client.Object
	for _, list := range []client.ObjectList{
		new(appsv1.StatefulSetList),
		new(corev1.ServiceList),
		new(cm.IssuerList),
		new(cm.CertificateList),
	} {
		require.NoError(t, r.client.List(ctx, list))
		items, err := meta.ExtractList(list)
		require.NoError(t, err)
		for _, item := range items {
			out = append(out, item.(client.Object))
		}
	}

	return marshalUpgradedObjects(t, r.scheme, out)
}

func marshalUpgradedObjects(t *testing.T, scheme *runtime.Scheme, objs []client.Object) []byte {
	t.Helper()

	volatile := []string{
		"percona.com/last-config-hash",
		naming.AnnotationSSLHash,
		naming.AnnotationSSLInternalHash,
		naming.AnnotationPBMTLSHash,
	}

	var buf bytes.Buffer
	for _, obj := range objs {
		gvk, err := apiutil.GVKForObject(obj, scheme)
		require.NoError(t, err)
		obj.GetObjectKind().SetGroupVersionKind(gvk)
		obj.SetResourceVersion("")

		ann := obj.GetAnnotations()
		for _, k := range volatile {
			delete(ann, k)
		}
		obj.SetAnnotations(ann)
		if sts, ok := obj.(*appsv1.StatefulSet); ok {
			for _, k := range volatile {
				delete(sts.Spec.Template.Annotations, k)
			}
		}

		data, err := yaml.Marshal(obj)
		require.NoError(t, err)
		buf.WriteString("---\n")
		buf.Write(data)
	}
	return buf.Bytes()
}
