package perconaservermongodb

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/stretchr/testify/assert"
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

		for i := range svcList.Items {
			svcList.Items[i].APIVersion = "v1"
			svcList.Items[i].Kind = "Service"
			delete(svcList.Items[i].Annotations, "percona.com/last-config-hash")
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
		cr.Spec.Sharding.Enabled = false
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

func TestRemoveOutdatedServices(t *testing.T) {
	ctx := t.Context()
	const ns = "rs-svc-outdated"
	const crName = ns
	const rsName = "rs0"

	// names of all per-pod external services that may exist for the replset,
	// covering every component type. removeOutdatedServices is expected to keep
	// the ones backing currently-enabled members and delete the rest.
	prefix := crName + "-" + rsName
	baseSvcs := []string{prefix + "-0", prefix + "-1", prefix + "-2"}
	nonVotingSvcs := []string{
		prefix + "-" + naming.ComponentNonVotingShort + "-0",
		prefix + "-" + naming.ComponentNonVotingShort + "-1",
		prefix + "-" + naming.ComponentNonVotingShort + "-2",
	}
	hiddenSvcs := []string{
		prefix + "-" + naming.ComponentHidden + "-0",
		prefix + "-" + naming.ComponentHidden + "-1",
	}
	arbiterSvcs := []string{prefix + "-" + naming.ComponentArbiter + "-0"}

	concat := func(lists ...[]string) []string {
		out := []string{}
		for _, l := range lists {
			out = append(out, l...)
		}
		sort.Strings(out)
		return out
	}

	allSvcs := concat(baseSvcs, nonVotingSvcs, hiddenSvcs, arbiterSvcs)

	// seedServices builds external services (with the labels removeOutdatedServices
	// selects on) for every name in allSvcs.
	seedServices := func(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) []client.Object {
		objs := make([]client.Object, 0, len(allSvcs))
		for _, name := range allSvcs {
			objs = append(objs, psmdb.ExternalService(cr, rs, name))
		}
		return objs
	}

	remainingServices := func(t *testing.T, cl client.Client) []string {
		t.Helper()

		svcList := new(corev1.ServiceList)
		if err := cl.List(ctx, svcList, client.InNamespace(ns)); err != nil {
			t.Fatal(err)
		}
		names := make([]string, 0, len(svcList.Items))
		for _, svc := range svcList.Items {
			names = append(names, svc.Name)
		}
		sort.Strings(names)
		return names
	}

	tests := []struct {
		name      string
		configure func(rs *api.ReplsetSpec)
		pause     bool
		want      []string
	}{
		{
			name: "exposed replset without special members keeps only base services",
			configure: func(rs *api.ReplsetSpec) {
				rs.Expose.Enabled = true
			},
			want: baseSvcs,
		},
		{
			name: "not exposed replset removes all services",
			configure: func(rs *api.ReplsetSpec) {
				rs.Expose.Enabled = false
				rs.NonVoting.Enabled = true
				rs.Hidden.Enabled = true
				rs.Arbiter.Enabled = true
			},
			want: nil,
		},
		{
			name: "exposed replset with hidden members keeps base and hidden services",
			configure: func(rs *api.ReplsetSpec) {
				rs.Expose.Enabled = true
				rs.Hidden.Enabled = true
				rs.Hidden.Size = 2
			},
			want: concat(baseSvcs, hiddenSvcs),
		},
		{
			name: "exposed replset with non-voting members keeps base and non-voting services",
			configure: func(rs *api.ReplsetSpec) {
				rs.Expose.Enabled = true
				rs.NonVoting.Enabled = true
				rs.NonVoting.Size = 3
			},
			want: concat(baseSvcs, nonVotingSvcs),
		},
		{
			name: "exposed replset with arbiter keeps base and arbiter services",
			configure: func(rs *api.ReplsetSpec) {
				rs.Expose.Enabled = true
				rs.Arbiter.Enabled = true
				rs.Arbiter.Size = 1
			},
			want: concat(baseSvcs, arbiterSvcs),
		},
		{
			name: "all member types enabled keeps every service",
			configure: func(rs *api.ReplsetSpec) {
				rs.Expose.Enabled = true
				rs.Hidden.Enabled = true
				rs.Hidden.Size = 2
				rs.NonVoting.Enabled = true
				rs.NonVoting.Size = 3
				rs.Arbiter.Enabled = true
				rs.Arbiter.Size = 1
			},
			want: allSvcs,
		},
		{
			name: "paused cluster keeps all services untouched",
			configure: func(rs *api.ReplsetSpec) {
				rs.Expose.Enabled = true
			},
			pause: true,
			want:  allSvcs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr, err := readDefaultCR(crName, ns)
			if err != nil {
				t.Fatal(err)
			}
			cr.Spec.Sharding.Enabled = false
			if err := cr.CheckNSetDefaults(ctx, ""); err != nil {
				t.Fatal(err)
			}

			rs := cr.Spec.Replsets[0]
			rs.Size = 3
			tt.configure(rs)
			cr.Spec.Pause = tt.pause

			objs := append([]client.Object{cr}, seedServices(cr, rs)...)
			r := buildFakeClient(objs...)

			if err := r.removeOutdatedServices(ctx, cr, rs); err != nil {
				t.Fatal(err)
			}

			got := remainingServices(t, r.client)
			want := tt.want
			sort.Strings(want)
			assert.Equal(t, len(want), len(got), "unexpected remaining service count")
			assert.ElementsMatch(t, want, got, "unexpected remaining services")
		})
	}
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
	if !bytes.Equal(bytes.TrimSpace(data), bytes.TrimSpace(expected)) {
		t.Fatalf("yaml resources doesn't match:\nexpected:\n%s\ngot:\n%s", string(expected), string(data))
	}
}
