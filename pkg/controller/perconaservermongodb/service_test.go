package perconaservermongodb

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func TestReconcileReplsetServices(t *testing.T) {
	ctx := t.Context()
	const ns = "rs-svc"
	const crName = ns

	getReplsets := func(t *testing.T, cr *api.PerconaServerMongoDB) []*api.ReplsetSpec {
		t.Helper()

		cr = cr.DeepCopy()
		repls := cr.Spec.Replsets
		if cr.Spec.Sharding.Enabled && cr.Spec.Sharding.ConfigsvrReplSet != nil {
			repls = append([]*api.ReplsetSpec{cr.Spec.Sharding.ConfigsvrReplSet}, repls...)
		}
		return repls
	}

	compareSvcList := func(t *testing.T, cl client.Client, cr *api.PerconaServerMongoDB, filename string) {
		t.Helper()

		svcList := new(corev1.ServiceList)
		if err := cl.List(ctx, svcList, client.InNamespace(cr.Namespace)); err != nil {
			t.Fatal(err)
		}

		yamlCompare(t, ns, filename, svcList)
	}

	prepareObjects := func(t *testing.T, cr *api.PerconaServerMongoDB) []client.Object {
		t.Helper()

		repls := getReplsets(t, cr)

		objs := []client.Object{cr}
		for _, rs := range repls {
			component := naming.ComponentMongod
			if rs.ClusterRole == api.ClusterRoleConfigSvr {
				component = api.ConfigReplSetName
			}
			objs = append(objs, fakeStatefulset(cr, rs, rs.Size, "", component))
			objs = append(objs, fakePodsForRS(cr, rs)...)
		}
		return objs
	}

	t.Run("expose toggle: not sharded cluster", func(t *testing.T) {
		cr, err := readDefaultCR(crName, ns)
		if err != nil {
			t.Fatal(err)
		}
		cr.Spec.Replsets[0].Expose.Enabled = false
		if err := cr.CheckNSetDefaults(ctx, ""); err != nil {
			t.Fatal(err)
		}
		r := buildFakeClient(prepareObjects(t, cr)...)

		if err := r.reconcileReplsetServices(ctx, cr, getReplsets(t, cr)); err != nil {
			t.Fatal(err)
		}
		compareSvcList(t, r.client, cr, "svc_list_expose_off.yaml")

		cr.Spec.Replsets[0].Expose.Enabled = true
		if err := r.client.Update(ctx, cr); err != nil {
			t.Fatal(err)
		}
		if err := r.reconcileReplsetServices(ctx, cr, getReplsets(t, cr)); err != nil {
			t.Fatal(err)
		}
		compareSvcList(t, r.client, cr, "svc_list_expose_on.yaml")

		cr.Spec.Replsets[0].Expose.Enabled = false
		if err := r.client.Update(ctx, cr); err != nil {
			t.Fatal(err)
		}
		if err := r.reconcileReplsetServices(ctx, cr, getReplsets(t, cr)); err != nil {
			t.Fatal(err)
		}
		compareSvcList(t, r.client, cr, "svc_list_expose_off.yaml")
	})

	t.Run("expose toggle: sharded cluster", func(t *testing.T) {
		cr, err := readDefaultCR(crName, ns)
		if err != nil {
			t.Fatal(err)
		}
		cr.Spec.Replsets[0].Expose.Enabled = false
		cr.Spec.Sharding.Enabled = true
		if err := cr.CheckNSetDefaults(ctx, ""); err != nil {
			t.Fatal(err)
		}

		r := buildFakeClient(prepareObjects(t, cr)...)

		if err := r.reconcileReplsetServices(ctx, cr, getReplsets(t, cr)); err != nil {
			t.Fatal(err)
		}
		compareSvcList(t, r.client, cr, "svc_list_sharded_expose_off.yaml")

		cr.Spec.Replsets[0].Expose.Enabled = true
		if err := r.client.Update(ctx, cr); err != nil {
			t.Fatal(err)
		}
		if err := r.reconcileReplsetServices(ctx, cr, getReplsets(t, cr)); err != nil {
			t.Fatal(err)
		}
		compareSvcList(t, r.client, cr, "svc_list_sharded_expose_on.yaml")

		cr.Spec.Replsets[0].Expose.Enabled = false
		if err := r.client.Update(ctx, cr); err != nil {
			t.Fatal(err)
		}
		if err := r.reconcileReplsetServices(ctx, cr, getReplsets(t, cr)); err != nil {
			t.Fatal(err)
		}
		compareSvcList(t, r.client, cr, "svc_list_sharded_expose_off.yaml")
	})
}

func yamlCompare(t *testing.T, ns string, filename string, compare any) {
	t.Helper()

	data, err := yaml.Marshal(compare)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile(filepath.Join("testdata", ns, filename))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Compare(data, expected) != 0 {
		t.Fatalf("yaml resources doesn't match:\nexpected:\n%s\ngot:\n%s", string(expected), string(data))
	}
}
