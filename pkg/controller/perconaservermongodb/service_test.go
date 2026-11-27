package perconaservermongodb

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconcileReplsetServices(t *testing.T) {
	t.Parallel()

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
			objs = append(objs, fakeStatefulset(cr, rs, rs.Size, "", component))
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
	t.Parallel()

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
			require.NoError(t, err)

			cr.Spec.Sharding.Enabled = false
			require.NoError(t, cr.CheckNSetDefaults(ctx, ""))

			rs := cr.Spec.Replsets[0]
			rs.Size = 3
			tt.configure(rs)
			cr.Spec.Pause = tt.pause

			objs := append([]client.Object{cr}, seedServices(cr, rs)...)
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
	t.Parallel()

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

func TestServiceIgnoreLabelsAnnotations(t *testing.T) {
	t.Parallel()

	type meta = map[string]string
	type step struct {
		name string
		// manual is added to the service by hand before the reconcile
		manual            meta
		ignore            bool
		crLabels, crAnnot meta
		wantLabels        meta
		wantAnnot         meta
	}
	steps := []step{
		{
			name:       "manual metadata kept without CR metadata",
			manual:     meta{"ignoredLabel": "true", "notIgnoredLabel": "true"},
			wantLabels: meta{"ignoredLabel": "true", "notIgnoredLabel": "true"},
			wantAnnot:  meta{"ignoredAnnotation": "true", "notIgnoredAnnotation": "true"},
		},
		{
			name:       "ignore lists keep manual metadata",
			ignore:     true,
			wantLabels: meta{"ignoredLabel": "true", "notIgnoredLabel": "true"},
			wantAnnot:  meta{"ignoredAnnotation": "true", "notIgnoredAnnotation": "true"},
		},
		{
			name:       "CR metadata drops not ignored keys",
			ignore:     true,
			crLabels:   meta{"crLabel": "true"},
			crAnnot:    meta{"crAnnotation": "true"},
			wantLabels: meta{"crLabel": "true", "ignoredLabel": "true"},
			wantAnnot:  meta{"crAnnotation": "true", "ignoredAnnotation": "true"},
		},
		{
			name:       "removed CR metadata stays",
			ignore:     true,
			wantLabels: meta{"crLabel": "true", "ignoredLabel": "true"},
			wantAnnot:  meta{"crAnnotation": "true", "ignoredAnnotation": "true"},
		},
		{
			name:       "new CR metadata replaces old",
			ignore:     true,
			crLabels:   meta{"otherCrLabel": "true", "secondCrLabel": "true"},
			crAnnot:    meta{"otherCrAnnotation": "true", "secondCrAnnotation": "true"},
			wantLabels: meta{"ignoredLabel": "true", "otherCrLabel": "true", "secondCrLabel": "true"},
			wantAnnot:  meta{"ignoredAnnotation": "true", "otherCrAnnotation": "true", "secondCrAnnotation": "true"},
		},
		{
			name:       "key removed from CR metadata is dropped",
			ignore:     true,
			crLabels:   meta{"otherCrLabel": "true"},
			crAnnot:    meta{"otherCrAnnotation": "true"},
			wantLabels: meta{"ignoredLabel": "true", "otherCrLabel": "true"},
			wantAnnot:  meta{"ignoredAnnotation": "true", "otherCrAnnotation": "true"},
		},
		{
			name:       "ignored keys dropped without ignore lists",
			crLabels:   meta{"otherCrLabel": "true"},
			crAnnot:    meta{"otherCrAnnotation": "true"},
			wantLabels: meta{"otherCrLabel": "true"},
			wantAnnot:  meta{"otherCrAnnotation": "true"},
		},
	}
	// the first step adds the same keys as annotations
	manualAnnot := meta{"ignoredAnnotation": "true", "notIgnoredAnnotation": "true"}

	userMeta := func(m meta) meta {
		out := meta{}
		for k, v := range m {
			if !strings.HasPrefix(k, "app.kubernetes.io/") && !strings.HasPrefix(k, "percona.com/") {
				out[k] = v
			}
		}
		return out
	}

	tests := map[string]struct {
		svc      string
		headless string
		expose   func(cr *api.PerconaServerMongoDB) *api.Expose
	}{
		"rs0": {
			svc:      "some-name-rs0-0",
			headless: "some-name-rs0",
			expose:   func(cr *api.PerconaServerMongoDB) *api.Expose { return &cr.Spec.Replsets[0].Expose.Expose },
		},
		"cfg": {
			svc:      "some-name-cfg-0",
			headless: "some-name-cfg",
			expose: func(cr *api.PerconaServerMongoDB) *api.Expose {
				return &cr.Spec.Sharding.ConfigsvrReplSet.Expose.Expose
			},
		},
		"mongos": {
			svc:    "some-name-mongos-0",
			expose: func(cr *api.PerconaServerMongoDB) *api.Expose { return &cr.Spec.Sharding.Mongos.Expose.Expose },
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			cr, err := readDefaultCR("some-name", "ignore-meta")
			require.NoError(t, err)
			cr.Spec.Replsets[0].Expose.Enabled = true
			cr.Spec.Sharding.ConfigsvrReplSet.Expose.Enabled = true
			cr.Spec.Sharding.Mongos.Expose.ServicePerPod = true
			require.NoError(t, cr.CheckNSetDefaults(ctx, ""))
			repls := []*api.ReplsetSpec{cr.Spec.Sharding.ConfigsvrReplSet, cr.Spec.Replsets[0]}

			objs := []client.Object{cr}
			for _, rs := range repls {
				component := naming.ComponentMongod
				if rs.ClusterRole == api.ClusterRoleConfigSvr {
					component = api.ConfigReplSetName
				}
				objs = append(objs, fakeStatefulset(cr, rs, rs.Size, "", component))
				objs = append(objs, fakePodsForRS(cr, rs)...)
			}
			r := buildFakeClient(objs...)
			require.NoError(t, r.reconcileServices(ctx, cr, repls))

			for _, s := range steps {
				if s.manual != nil {
					svc := new(corev1.Service)
					require.NoError(t, r.client.Get(ctx, client.ObjectKey{Name: tt.svc, Namespace: cr.Namespace}, svc))
					maps.Copy(svc.Labels, s.manual)
					if svc.Annotations == nil {
						svc.Annotations = meta{}
					}
					maps.Copy(svc.Annotations, manualAnnot)
					require.NoError(t, r.client.Update(ctx, svc))
				}

				cr.Spec.IgnoreLabels, cr.Spec.IgnoreAnnotations = nil, nil
				if s.ignore {
					cr.Spec.IgnoreLabels = []string{"ignoredLabel"}
					cr.Spec.IgnoreAnnotations = []string{"ignoredAnnotation"}
				}
				expose := tt.expose(cr)
				expose.ServiceLabels, expose.ServiceAnnotations = s.crLabels, s.crAnnot
				require.NoError(t, r.reconcileServices(ctx, cr, repls))

				svc := new(corev1.Service)
				require.NoError(t, r.client.Get(ctx, client.ObjectKey{Name: tt.svc, Namespace: cr.Namespace}, svc))
				assert.Equal(t, s.wantLabels, userMeta(svc.Labels), s.name)
				assert.Equal(t, s.wantAnnot, userMeta(svc.Annotations), s.name)

				if tt.headless == "" {
					continue
				}
				headless := new(corev1.Service)
				require.NoError(t, r.client.Get(ctx, client.ObjectKey{Name: tt.headless, Namespace: cr.Namespace}, headless))
				for k := range s.crAnnot {
					assert.NotContains(t, headless.Annotations, k, s.name)
				}
				for k, v := range s.crLabels {
					assert.Equal(t, v, headless.Labels[k], s.name)
				}
			}
		})
	}
}

func TestReconcileMongosSvc(t *testing.T) {
	tests := map[string]struct {
		sharding      bool
		servicePerPod bool
		want          []string
	}{
		"sharding disabled": {
			sharding: false,
		},
		"single service": {
			sharding: true,
			want:     []string{"mongos-svc-mongos"},
		},
		"service per pod": {
			sharding:      true,
			servicePerPod: true,
			want:          []string{"mongos-svc-mongos-0", "mongos-svc-mongos-1", "mongos-svc-mongos-2"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr, err := readDefaultCR("mongos-svc", "mongos-svc")
			require.NoError(t, err)
			cr.Spec.Sharding.Enabled = tt.sharding
			cr.Spec.Sharding.Mongos.Size = 3
			cr.Spec.Sharding.Mongos.Expose.ServicePerPod = tt.servicePerPod
			require.NoError(t, cr.CheckNSetDefaults(t.Context(), version.PlatformKubernetes))

			r := buildFakeClient(cr)
			require.NoError(t, r.reconcileMongosSvc(t.Context(), cr))

			svcs := new(corev1.ServiceList)
			require.NoError(t, r.client.List(t.Context(), svcs, client.InNamespace(cr.Namespace)))
			var got []string
			for _, svc := range svcs.Items {
				got = append(got, svc.Name)
			}
			sort.Strings(got)
			assert.Equal(t, tt.want, got)
		})
	}
}
