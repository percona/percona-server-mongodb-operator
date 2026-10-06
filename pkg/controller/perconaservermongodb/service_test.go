package perconaservermongodb

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		err := cl.List(ctx, svcList, client.InNamespace(cr.Namespace))
		require.NoError(t, err)

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
			objs = append(objs, fakeStatefulset(cr, rs, rs.GetMongodSize(), "", component))
			objs = append(objs, fakePodsForRS(cr, rs)...)
		}
		return objs
	}

	t.Run("expose toggle: not sharded cluster", func(t *testing.T) {
		cr, err := readDefaultCR(crName, ns)
		require.NoError(t, err)

		cr.Spec.Replsets[0].Expose.Enabled = false
		cr.Spec.Sharding.Enabled = false
		require.NoError(t, cr.CheckNSetDefaults(ctx, ""))

		r := buildFakeClient(prepareObjects(t, cr)...)

		require.NoError(t, r.reconcileReplsetServices(ctx, cr, getReplsets(t, cr)))
		compareSvcList(t, r.client, cr, "svc_list_expose_off.yaml")

		cr.Spec.Replsets[0].Expose.Enabled = true
		require.NoError(t, r.client.Update(ctx, cr))
		require.NoError(t, r.reconcileReplsetServices(ctx, cr, getReplsets(t, cr)))
		compareSvcList(t, r.client, cr, "svc_list_expose_on.yaml")

		cr.Spec.Replsets[0].Expose.Enabled = false
		require.NoError(t, r.client.Update(ctx, cr))
		require.NoError(t, r.reconcileReplsetServices(ctx, cr, getReplsets(t, cr)))
		compareSvcList(t, r.client, cr, "svc_list_expose_off.yaml")
	})

	t.Run("expose toggle: sharded cluster", func(t *testing.T) {
		cr, err := readDefaultCR(crName, ns)
		require.NoError(t, err)

		cr.Spec.Replsets[0].Expose.Enabled = false
		cr.Spec.Sharding.Enabled = true
		require.NoError(t, cr.CheckNSetDefaults(ctx, ""))

		r := buildFakeClient(prepareObjects(t, cr)...)

		require.NoError(t, r.reconcileReplsetServices(ctx, cr, getReplsets(t, cr)))
		compareSvcList(t, r.client, cr, "svc_list_sharded_expose_off.yaml")

		cr.Spec.Replsets[0].Expose.Enabled = true
		require.NoError(t, r.client.Update(ctx, cr))
		require.NoError(t, r.reconcileReplsetServices(ctx, cr, getReplsets(t, cr)))
		compareSvcList(t, r.client, cr, "svc_list_sharded_expose_on.yaml")

		cr.Spec.Replsets[0].Expose.Enabled = false
		require.NoError(t, r.client.Update(ctx, cr))
		require.NoError(t, r.reconcileReplsetServices(ctx, cr, getReplsets(t, cr)))
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
		// pods that exist while removeOutdatedServices runs. A per-pod service
		// is named after its pod, so these are service names too.
		pods []string
		want []string
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
			name: "a scale-down victim keeps its service while its pod is up",
			configure: func(rs *api.ReplsetSpec) {
				rs.Expose.Enabled = true
				rs.Size = new(int32(2))
			},
			pods: []string{prefix + "-2"},
			want: baseSvcs,
		},
		{
			name: "a scale-down victim loses its service once the pod is gone",
			configure: func(rs *api.ReplsetSpec) {
				rs.Expose.Enabled = true
				rs.Size = new(int32(2))
			},
			want: []string{prefix + "-0", prefix + "-1"},
		},
		{
			name: "turning expose off deletes per-pod services even while the pods are up",
			configure: func(rs *api.ReplsetSpec) {
				rs.Expose.Enabled = false
			},
			pods: baseSvcs,
			want: nil,
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
			require.NoError(t, err)

			cr.Spec.Sharding.Enabled = false
			require.NoError(t, cr.CheckNSetDefaults(ctx, ""))

			rs := cr.Spec.Replsets[0]
			rs.Size = new(int32(3))
			tt.configure(rs)
			cr.Spec.Pause = tt.pause

			objs := append([]client.Object{cr}, seedServices(cr, rs)...)
			for _, name := range tt.pods {
				objs = append(objs, &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
				})
			}
			r := buildFakeClient(objs...)

			require.NoError(t, r.removeOutdatedServices(ctx, cr, rs))

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

func TestRemoveStaleExternalDNSAnnotations(t *testing.T) {
	tests := map[string]struct {
		old      map[string]string
		desired  map[string]string
		expected map[string]string
	}{
		"externalDNS removed from CR: managed annotations dropped": {
			old: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": "prod-rs0-0.mongo.example.com",
				"external-dns.alpha.kubernetes.io/ttl":      "300",
				"percona.com/external-dns-managed":          "true",
				"cloud.google.com/neg":                      `{"ingress":true}`,
			},
			desired: map[string]string{},
			expected: map[string]string{
				"cloud.google.com/neg": `{"ingress":true}`,
			},
		},
		"ttl removed from CR: stale ttl dropped, hostname kept": {
			old: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": "prod-rs0-0.mongo.example.com",
				"external-dns.alpha.kubernetes.io/ttl":      "300",
				"percona.com/external-dns-managed":          "true",
			},
			desired: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": "prod-rs0-0.mongo.example.com",
				"percona.com/external-dns-managed":          "true",
			},
			expected: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": "prod-rs0-0.mongo.example.com",
				"percona.com/external-dns-managed":          "true",
			},
		},
		"manually added external-dns annotations (no marker) are preserved": {
			old: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": "manual.example.com",
				"external-dns.alpha.kubernetes.io/ttl":      "60",
			},
			desired: map[string]string{},
			expected: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": "manual.example.com",
				"external-dns.alpha.kubernetes.io/ttl":      "60",
			},
		},
		"externalDNS enabled over manual annotations: operator takes ownership": {
			old: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": "manual.example.com",
			},
			desired: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": "prod-rs0-0.mongo.example.com",
				"percona.com/external-dns-managed":          "true",
			},
			expected: map[string]string{
				"external-dns.alpha.kubernetes.io/hostname": "manual.example.com",
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			removeStaleExternalDNSAnnotations(tt.old, tt.desired)
			assert.Equal(t, tt.expected, tt.old)
		})
	}
}

func TestExpectedExternalServiceNames(t *testing.T) {
	for _, tt := range []struct {
		name      string
		legacy    func(*api.PerconaServerMongoDB)
		instances []api.InstanceSpec
		expose    bool
		want      []string
	}{
		{
			name: "expose disabled contributes nothing at all",
			legacy: func(c *api.PerconaServerMongoDB) {
				c.Spec.Replsets[0].Size = new(int32(3))
				c.Spec.Replsets[0].NonVoting = api.NonVotingSpec{Enabled: true, Size: 2}
				c.Spec.Replsets[0].Hidden = api.HiddenSpec{Enabled: true, Size: 1}
			},
			expose: false,
			want:   []string{},
		},
		{
			name: "every legacy role contributes its pods",
			legacy: func(c *api.PerconaServerMongoDB) {
				c.Spec.Replsets[0].Size = new(int32(3))
				c.Spec.Replsets[0].NonVoting = api.NonVotingSpec{Enabled: true, Size: 2}
				c.Spec.Replsets[0].Hidden = api.HiddenSpec{Enabled: true, Size: 1}
				c.Spec.Replsets[0].Arbiter = api.Arbiter{Enabled: true, Size: 1}
				c.Spec.Unsafe.ReplsetSize = true
			},
			expose: true,
			want: []string{
				"svc-cr-rs0-0", "svc-cr-rs0-1", "svc-cr-rs0-2",
				"svc-cr-rs0-arbiter-0",
				"svc-cr-rs0-hidden-0",
				"svc-cr-rs0-nv-0", "svc-cr-rs0-nv-1",
			},
		},
		{
			name: "every instance group contributes its pods",
			instances: []api.InstanceSpec{
				voting("mongod", 2), voting("hot", 2), voting("cold", 1),
			},
			expose: true,
			want: []string{
				"svc-cr-rs0-0", "svc-cr-rs0-1",
				"svc-cr-rs0-cold-0",
				"svc-cr-rs0-hot-0", "svc-cr-rs0-hot-1",
			},
		},
		{
			name: "a group scaled to zero contributes nothing",
			instances: []api.InstanceSpec{
				voting("mongod", 3), voting("empty", 0),
			},
			expose: true,
			want:   []string{"svc-cr-rs0-0", "svc-cr-rs0-1", "svc-cr-rs0-2"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var cr *api.PerconaServerMongoDB
			if tt.instances != nil {
				cr = instanceCR(t, "svc-cr", "svc", tt.instances, unsafeSize)
			} else {
				cr = legacyCR(t, "svc-cr", "svc", tt.legacy)
			}
			rs := cr.Spec.Replsets[0]
			rs.Expose.Enabled = tt.expose

			set, err := membergroup.Resolve(cr, rs)
			require.NoError(t, err)

			r := buildFakeClient(cr)

			assert.Equal(t, tt.want, r.expectedExternalServiceNames(cr, rs, set))
		})
	}
}

func TestRemoveOutdatedServicesInstanceMode(t *testing.T) {
	ctx := t.Context()

	instances := []api.InstanceSpec{voting("mongod", 2), voting("hot", 2)}
	cr := instanceCR(t, "svc-cr", "svc", instances, unsafeSize)
	rs := cr.Spec.Replsets[0]
	rs.Expose.Enabled = true

	seeded := []string{
		"svc-cr-rs0-0", "svc-cr-rs0-1",
		"svc-cr-rs0-hot-0", "svc-cr-rs0-hot-1", "svc-cr-rs0-hot-2",
	}

	objs := []client.Object{cr}
	for _, name := range seeded {
		objs = append(objs, psmdb.ExternalService(cr, rs, name))
	}
	// the shrunk group's pod is already gone, so its service is a leftover
	for _, name := range []string{"svc-cr-rs0-0", "svc-cr-rs0-1", "svc-cr-rs0-hot-0", "svc-cr-rs0-hot-1"} {
		objs = append(objs, &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cr.Namespace},
		})
	}

	r := buildFakeClient(objs...)
	require.NoError(t, r.removeOutdatedServices(ctx, cr, rs))

	svcList := new(corev1.ServiceList)
	require.NoError(t, r.client.List(ctx, svcList, client.InNamespace(cr.Namespace)))

	got := make([]string, 0, len(svcList.Items))
	for i := range svcList.Items {
		got = append(got, svcList.Items[i].Name)
	}
	sort.Strings(got)

	assert.Equal(t, []string{
		"svc-cr-rs0-0", "svc-cr-rs0-1", "svc-cr-rs0-hot-0", "svc-cr-rs0-hot-1",
	}, got, "the service of the retired ordinal is deleted, the rest are kept")
}

func TestRemoveOutdatedServicesToleratesAMissingService(t *testing.T) {
	ctx := t.Context()

	cr := instanceCR(t, "svc-cr", "svc", []api.InstanceSpec{voting("mongod", 2)}, unsafeSize)
	rs := cr.Spec.Replsets[0]
	rs.Expose.Enabled = true

	stale := psmdb.ExternalService(cr, rs, "svc-cr-rs0-9")
	r := buildFakeClient(cr, stale)

	// first pass deletes it
	require.NoError(t, r.removeOutdatedServices(ctx, cr, rs))
	// second pass sees nothing and must not error
	require.NoError(t, r.removeOutdatedServices(ctx, cr, rs))
}
