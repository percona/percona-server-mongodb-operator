package perconaservermongodb

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

// rl builds a ResourceList from cpu/memory quantity strings.
// An empty string omits that resource.
func rl(cpu, mem string) corev1.ResourceList {
	out := corev1.ResourceList{}
	if cpu != "" {
		out[corev1.ResourceCPU] = resource.MustParse(cpu)
	}
	if mem != "" {
		out[corev1.ResourceMemory] = resource.MustParse(mem)
	}
	return out
}

// assertResourceList compares two ResourceLists by quantity value rather than by
// struct equality. resource.Quantity caches its source formatting in unexported
// fields, so a parsed "4Gi" and a computed 4294967296 are equal in value but not
// with assert.Equal.
func assertResourceList(t *testing.T, expected, actual corev1.ResourceList) {
	t.Helper()
	assert.Len(t, actual, len(expected))
	for res, exp := range expected {
		got, ok := actual[res]
		if !assert.Truef(t, ok, "expected resource %s to be present", res) {
			continue
		}
		assert.Truef(t, exp.Cmp(got) == 0, "resource %s: expected %s, got %s", res, exp.String(), got.String())
	}
}

func TestClampRecommendation(t *testing.T) {
	tests := []struct {
		name     string
		rec      corev1.ResourceList
		bounds   vpaBounds
		expected corev1.ResourceList
	}{
		{
			name:     "no bounds leaves recommendation untouched",
			rec:      rl("500m", "1Gi"),
			bounds:   vpaBounds{},
			expected: rl("500m", "1Gi"),
		},
		{
			name:     "raises values below minAllowed",
			rec:      rl("50m", "128Mi"),
			bounds:   vpaBounds{minAllowed: rl("100m", "256Mi")},
			expected: rl("100m", "256Mi"),
		},
		{
			name:     "lowers values above maxAllowed",
			rec:      rl("16", "32Gi"),
			bounds:   vpaBounds{maxAllowed: rl("8", "16Gi")},
			expected: rl("8", "16Gi"),
		},
		{
			name:     "values within bounds are unchanged",
			rec:      rl("2", "4Gi"),
			bounds:   vpaBounds{minAllowed: rl("100m", "256Mi"), maxAllowed: rl("8", "16Gi")},
			expected: rl("2", "4Gi"),
		},
		{
			name:     "value exactly at minAllowed is unchanged",
			rec:      rl("100m", "256Mi"),
			bounds:   vpaBounds{minAllowed: rl("100m", "256Mi")},
			expected: rl("100m", "256Mi"),
		},
		{
			name:     "value exactly at maxAllowed is unchanged",
			rec:      rl("8", "16Gi"),
			bounds:   vpaBounds{maxAllowed: rl("8", "16Gi")},
			expected: rl("8", "16Gi"),
		},
		{
			name:     "each resource is clamped independently",
			rec:      rl("50m", "32Gi"),
			bounds:   vpaBounds{minAllowed: rl("100m", "256Mi"), maxAllowed: rl("8", "16Gi")},
			expected: rl("100m", "16Gi"),
		},
		{
			name:     "resource without a bound passes through",
			rec:      rl("50m", "1Gi"),
			bounds:   vpaBounds{minAllowed: rl("100m", "")},
			expected: rl("100m", "1Gi"),
		},
		{
			name:     "bound for a resource absent from the recommendation is ignored",
			rec:      rl("500m", ""),
			bounds:   vpaBounds{minAllowed: rl("", "256Mi"), maxAllowed: rl("", "16Gi")},
			expected: rl("500m", ""),
		},
		{
			name:     "empty recommendation stays empty",
			rec:      corev1.ResourceList{},
			bounds:   vpaBounds{minAllowed: rl("100m", "256Mi"), maxAllowed: rl("8", "16Gi")},
			expected: corev1.ResourceList{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertResourceList(t, tt.expected, clampRecommendation(tt.rec, tt.bounds))
		})
	}
}

func TestClampRecommendationDoesNotMutateInput(t *testing.T) {
	rec := rl("50m", "32Gi")
	bounds := vpaBounds{minAllowed: rl("100m", "256Mi"), maxAllowed: rl("8", "16Gi")}

	clamped := clampRecommendation(rec, bounds)

	assertResourceList(t, rl("50m", "32Gi"), rec)
	assertResourceList(t, rl("100m", "16Gi"), clamped)
}

func TestCapRequestsAtLimits(t *testing.T) {
	tests := []struct {
		name     string
		requests corev1.ResourceList
		limits   corev1.ResourceList
		expected corev1.ResourceList
	}{
		{
			name:     "nil limits leaves requests unchanged",
			requests: rl("4", "8Gi"),
			limits:   nil,
			expected: rl("4", "8Gi"),
		},
		{
			name:     "empty limits leaves requests unchanged",
			requests: rl("4", "8Gi"),
			limits:   corev1.ResourceList{},
			expected: rl("4", "8Gi"),
		},
		{
			name:     "request above its limit is capped to the limit",
			requests: rl("4", "8Gi"),
			limits:   rl("2", "4Gi"),
			expected: rl("2", "4Gi"),
		},
		{
			name:     "request below its limit is unchanged",
			requests: rl("1", "2Gi"),
			limits:   rl("2", "4Gi"),
			expected: rl("1", "2Gi"),
		},
		{
			name:     "request equal to its limit is unchanged",
			requests: rl("2", "4Gi"),
			limits:   rl("2", "4Gi"),
			expected: rl("2", "4Gi"),
		},
		{
			name:     "only the exceeding resource is capped",
			requests: rl("4", "2Gi"),
			limits:   rl("2", "4Gi"),
			expected: rl("2", "2Gi"),
		},
		{
			name:     "resource without a matching limit is untouched",
			requests: rl("4", "8Gi"),
			limits:   rl("", "4Gi"),
			expected: rl("4", "4Gi"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertResourceList(t, tt.expected, capRequestsAtLimits(tt.requests, tt.limits))
		})
	}
}

func TestCapRequestsAtLimitsDoesNotMutateInput(t *testing.T) {
	requests := rl("4", "8Gi")
	limits := rl("2", "4Gi")

	capped := capRequestsAtLimits(requests, limits)

	assertResourceList(t, rl("4", "8Gi"), requests)
	assertResourceList(t, rl("2", "4Gi"), capped)
}

func TestRatioScaledLimits(t *testing.T) {
	tests := []struct {
		name        string
		orig        corev1.ResourceRequirements
		newRequests corev1.ResourceList
		expected    corev1.ResourceList
	}{
		{
			name:        "nil original limits returns nil",
			orig:        corev1.ResourceRequirements{Requests: rl("1", "1Gi")},
			newRequests: rl("2", "2Gi"),
			expected:    nil,
		},
		{
			name: "preserves a 2:1 cpu ratio",
			orig: corev1.ResourceRequirements{
				Requests: rl("1", ""),
				Limits:   rl("2", ""),
			},
			newRequests: rl("2", ""),
			expected:    rl("4", ""),
		},
		{
			name: "preserves a 2:1 memory ratio",
			orig: corev1.ResourceRequirements{
				Requests: rl("", "1Gi"),
				Limits:   rl("", "2Gi"),
			},
			newRequests: rl("", "2Gi"),
			expected:    rl("", "4Gi"),
		},
		{
			name: "a 1:1 ratio yields a limit equal to the new request",
			orig: corev1.ResourceRequirements{
				Requests: rl("1", "1Gi"),
				Limits:   rl("1", "1Gi"),
			},
			newRequests: rl("3", "3Gi"),
			expected:    rl("3", "3Gi"),
		},
		{
			name: "preserves a fractional 1.5:1 ratio",
			orig: corev1.ResourceRequirements{
				Requests: rl("1", ""),
				Limits:   rl("1500m", ""),
			},
			newRequests: rl("2", ""),
			expected:    rl("3", ""),
		},
		{
			name: "scales cpu and memory independently",
			orig: corev1.ResourceRequirements{
				Requests: rl("1", "1Gi"),
				Limits:   rl("2", "4Gi"),
			},
			newRequests: rl("2", "2Gi"),
			expected:    rl("4", "8Gi"),
		},
		{
			name: "resource with no original request keeps its original limit",
			orig: corev1.ResourceRequirements{
				Requests: rl("1", ""),
				Limits:   rl("2", "4Gi"),
			},
			newRequests: rl("2", "2Gi"),
			expected:    rl("4", "4Gi"),
		},
		{
			name: "resource with no original limit is not added",
			orig: corev1.ResourceRequirements{
				Requests: rl("1", "1Gi"),
				Limits:   rl("2", ""),
			},
			newRequests: rl("2", "2Gi"),
			expected:    rl("4", ""),
		},
		{
			name: "zero original request is skipped rather than dividing by zero",
			orig: corev1.ResourceRequirements{
				Requests: rl("0", ""),
				Limits:   rl("2", ""),
			},
			newRequests: rl("1", ""),
			expected:    rl("2", ""),
		},
		{
			name: "new request for a resource absent from orig leaves limits untouched",
			orig: corev1.ResourceRequirements{
				Requests: rl("1", ""),
				Limits:   rl("2", ""),
			},
			newRequests: rl("", "2Gi"),
			expected:    rl("2", ""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ratioScaledLimits(tt.orig, tt.newRequests)
			if tt.expected == nil {
				assert.Nil(t, got)
				return
			}
			assertResourceList(t, tt.expected, got)
		})
	}
}

func TestRatioScaledLimitsDoesNotMutateInput(t *testing.T) {
	orig := corev1.ResourceRequirements{
		Requests: rl("1", "1Gi"),
		Limits:   rl("2", "4Gi"),
	}

	scaled := ratioScaledLimits(orig, rl("2", "2Gi"))

	require.NotNil(t, scaled)
	assertResourceList(t, rl("1", "1Gi"), orig.Requests)
	assertResourceList(t, rl("2", "4Gi"), orig.Limits)
	assertResourceList(t, rl("4", "8Gi"), scaled)
}

// vpaTestClient builds a real client whose RESTMapper either knows the
// VerticalPodAutoscaler kind or does not, modelling a cluster with or without the
// VPA CRD installed. The rest.Config points nowhere: these tests only exercise the
// mapping stage, which happens before any request is sent.
func vpaTestClient(t *testing.T, crdInstalled bool) client.Client {
	t.Helper()

	var mapper meta.RESTMapper
	if crdInstalled {
		m := meta.NewDefaultRESTMapper([]schema.GroupVersion{vpaGVK.GroupVersion()})
		m.Add(vpaGVK, meta.RESTScopeNamespace)
		mapper = m
	} else {
		mapper = meta.NewDefaultRESTMapper(nil)
	}

	c, err := client.New(&rest.Config{Host: "http://127.0.0.1:1"}, client.Options{
		Scheme: scheme.Scheme,
		Mapper: mapper,
	})
	require.NoError(t, err)
	return c
}

// vpaTestCR returns a CR with VPA enabled and no components, so reconcileVPA
// exercises only the CRD guard and never reads a VPA object.
func vpaTestCR(enabled bool) *api.PerconaServerMongoDB {
	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "some-name", Namespace: "ns"},
	}
	if enabled {
		cr.Spec.VPA = &api.VPASpec{Enabled: true, UpdateMode: api.VPAUpdateModeOff}
	}
	return cr
}

func TestReconcileVPACRDNotInstalled(t *testing.T) {
	r := &ReconcilePerconaServerMongoDB{client: vpaTestClient(t, false)}
	cr := vpaTestCR(true)

	// A missing CRD must not fail the reconcile: VPA is an optional integration.
	require.NoError(t, r.reconcileVPA(t.Context(), cr))

	cond := cr.Status.FindCondition(api.ConditionTypeVPAReady)
	require.NotNil(t, cond, "expected a VPAReady condition explaining why VPA is inert")
	assert.Equal(t, api.ConditionFalse, cond.Status)
	assert.Equal(t, "VPACRDNotInstalled", cond.Reason)
	assert.Contains(t, cond.Message, "autoscaling.k8s.io/v1")

	// Nothing was read, so no per-component status was recorded.
	assert.Empty(t, cr.Status.VPAStatus)
}

func TestReconcileVPACRDInstalled(t *testing.T) {
	r := &ReconcilePerconaServerMongoDB{client: vpaTestClient(t, true)}
	cr := vpaTestCR(true)

	require.NoError(t, r.reconcileVPA(t.Context(), cr))

	cond := cr.Status.FindCondition(api.ConditionTypeVPAReady)
	require.NotNil(t, cond)
	assert.Equal(t, api.ConditionTrue, cond.Status)
	assert.Equal(t, "VPACRDInstalled", cond.Reason)
}

func TestReconcileVPADisabledClearsCondition(t *testing.T) {
	r := &ReconcilePerconaServerMongoDB{client: vpaTestClient(t, false)}

	// Start from a cluster that previously reported the missing CRD.
	cr := vpaTestCR(true)
	require.NoError(t, r.reconcileVPA(t.Context(), cr))
	require.NotNil(t, cr.Status.FindCondition(api.ConditionTypeVPAReady))

	// Disabling VPA must retract the condition rather than leave it stale.
	cr.Spec.VPA.Enabled = false
	require.NoError(t, r.reconcileVPA(t.Context(), cr))
	assert.Nil(t, cr.Status.FindCondition(api.ConditionTypeVPAReady))
}

func TestReadVPARecommendationToleratesMissingCRD(t *testing.T) {
	r := &ReconcilePerconaServerMongoDB{client: vpaTestClient(t, false)}

	// A CRD uninstalled between the reconcile-level check and this read surfaces as
	// NoKindMatchError, which carries no NotFound status reason and so is not covered
	// by client.IgnoreNotFound. It must still be treated as "no recommendation".
	res, err := r.readVPARecommendation(t.Context(), "ns", "some-name-rs0-vpa", "mongod")

	assert.NoError(t, err)
	assert.Nil(t, res.rec)
	assert.Equal(t, vpaStateObjectNotFound, res.state)
}

// vpaObject builds a VerticalPodAutoscaler with the given container recommendations.
// A nil map omits status.recommendation entirely.
func vpaObject(name string, recs map[string]corev1.ResourceList) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(vpaGVK)
	obj.SetName(name)
	obj.SetNamespace("ns")
	if recs == nil {
		return obj
	}
	items := make([]interface{}, 0, len(recs))
	for container, rl := range recs {
		target := map[string]interface{}{}
		for res, q := range rl {
			target[string(res)] = q.String()
		}
		items = append(items, map[string]interface{}{
			"containerName": container,
			"target":        target,
		})
	}
	_ = unstructured.SetNestedSlice(
		obj.Object, items, "status", "recommendation", "containerRecommendations",
	)
	return obj
}

func vpaReaderWith(t *testing.T, objs ...client.Object) *ReconcilePerconaServerMongoDB {
	t.Helper()
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{vpaGVK.GroupVersion()})
	m.Add(vpaGVK, meta.RESTScopeNamespace)
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithRESTMapper(m).WithObjects(objs...).Build()
	return &ReconcilePerconaServerMongoDB{client: c}
}

func TestReadVPARecommendationStates(t *testing.T) {
	tests := []struct {
		name          string
		objects       []client.Object
		vpaName       string
		container     string
		expectedState vpaReadState
		messageHas    []string
	}{
		{
			name:          "object missing",
			vpaName:       "some-name-rs0-vpa",
			container:     "mongod",
			expectedState: vpaStateObjectNotFound,
			messageHas:    []string{`"some-name-rs0-vpa"`, "not found"},
		},
		{
			name:          "object present but carries no recommendation",
			objects:       []client.Object{vpaObject("some-name-rs0-vpa", nil)},
			vpaName:       "some-name-rs0-vpa",
			container:     "mongod",
			expectedState: vpaStateNoRecommendation,
			// This is the Goldilocks-targets-the-CR case; the message must point at targetRef.
			messageHas: []string{"no recommendation", "targetRef", "PerconaServerMongoDB"},
		},
		{
			name: "recommendations present but not for our container",
			objects: []client.Object{vpaObject("wrong-workload-vpa", map[string]corev1.ResourceList{
				"psmdb-client": rl("25m", "250Mi"),
			})},
			vpaName:       "wrong-workload-vpa",
			container:     "mongod",
			expectedState: vpaStateContainerNotFound,
			messageHas:    []string{"psmdb-client", `"mongod"`},
		},
		{
			name: "recommendation found for our container",
			objects: []client.Object{vpaObject("some-name-rs0-vpa", map[string]corev1.ResourceList{
				"mongod": rl("350m", "350Mi"),
			})},
			vpaName:       "some-name-rs0-vpa",
			container:     "mongod",
			expectedState: vpaStateOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := vpaReaderWith(t, tt.objects...)

			res, err := r.readVPARecommendation(t.Context(), "ns", tt.vpaName, tt.container)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedState, res.state)

			msg := res.message(tt.container)
			if tt.expectedState == vpaStateOK {
				assert.Empty(t, msg, "a successful read must not carry a message")
				assertResourceList(t, rl("350m", "350Mi"), res.rec)
				return
			}

			assert.Nil(t, res.rec)
			for _, want := range tt.messageHas {
				assert.Containsf(t, msg, want, "message should mention %q: %s", want, msg)
			}
		})
	}
}

func TestVPAStatusRecordsWhyNoRecommendation(t *testing.T) {
	// An enabled component with a missing VPA object must still get a status entry,
	// so that an absent entry means "not configured" rather than "silently broken".
	r := vpaReaderWith(t)
	cr := vpaTestCR(true)
	cr.Spec.Replsets = []*api.ReplsetSpec{{Name: "rs0", Size: 3}}

	require.NoError(t, r.reconcileVPA(t.Context(), cr))

	s, ok := cr.Status.VPAStatus["rs0"]
	require.True(t, ok, "expected a status entry for rs0 even with no recommendation")
	assert.Contains(t, s.Message, "not found")
	assert.Nil(t, s.LastAppliedAt)
}

func TestVPAStatusMessageClearedOnRecovery(t *testing.T) {
	cr := vpaTestCR(true)
	cr.Spec.Replsets = []*api.ReplsetSpec{{Name: "rs0", Size: 3}}

	// First pass: no VPA object, so the component records why.
	require.NoError(t, vpaReaderWith(t).reconcileVPA(t.Context(), cr))
	require.NotEmpty(t, cr.Status.VPAStatus["rs0"].Message)

	// Second pass: the object now carries a recommendation; the message must clear.
	withRec := vpaReaderWith(t, vpaObject("some-name-rs0-vpa", map[string]corev1.ResourceList{
		"mongod": rl("350m", "350Mi"),
	}))
	require.NoError(t, withRec.reconcileVPA(t.Context(), cr))

	s := cr.Status.VPAStatus["rs0"]
	assert.Empty(t, s.Message, "message must clear once a recommendation is read")
	assert.Equal(t, "350m", s.CPU)
}

func TestStabilizationWindowDefaultIsNonZero(t *testing.T) {
	// A zero window lets recommender jitter restart the cluster as fast as the
	// operator reconciles, so the CRD must supply a floor.
	path := "../../../config/crd/bases/psmdb.percona.com_perconaservermongodbs.yaml"
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var crd map[string]any
	require.NoError(t, yaml.Unmarshal(data, &crd))

	node := crd["spec"].(map[string]any)["versions"].([]any)[0].(map[string]any)
	node = node["schema"].(map[string]any)["openAPIV3Schema"].(map[string]any)
	node = node["properties"].(map[string]any)["spec"].(map[string]any)
	node = node["properties"].(map[string]any)["vpa"].(map[string]any)
	window := node["properties"].(map[string]any)["stabilizationWindow"].(map[string]any)

	assert.Equal(t, "5m", window["default"], "spec.vpa.stabilizationWindow must default to a non-zero window")
}

func TestVPAResourcesResolution(t *testing.T) {
	declared := corev1.ResourceRequirements{
		Requests: rl("100m", "100M"),
		Limits:   rl("4", "4Gi"),
	}
	committed := &corev1.ResourceRequirements{
		Requests: rl("350m", "350Mi"),
		Limits:   rl("4", "4Gi"),
	}

	tests := []struct {
		name      string
		vpa       *api.VPASpec
		status    map[string]api.VPAComponentStatus
		expectCPU string
	}{
		{
			name:      "vpa not configured falls back to the declared spec",
			expectCPU: "100m",
		},
		{
			name:      "vpa disabled falls back to the declared spec",
			vpa:       &api.VPASpec{Enabled: false, UpdateMode: api.VPAUpdateModeAuto},
			status:    map[string]api.VPAComponentStatus{"rs0": {Resources: committed}},
			expectCPU: "100m",
		},
		{
			name:      "Off observes only, so the declared spec still applies",
			vpa:       &api.VPASpec{Enabled: true, UpdateMode: api.VPAUpdateModeOff},
			status:    map[string]api.VPAComponentStatus{"rs0": {Resources: committed}},
			expectCPU: "100m",
		},
		{
			name:      "Auto with nothing committed yet falls back to the declared spec",
			vpa:       &api.VPASpec{Enabled: true, UpdateMode: api.VPAUpdateModeAuto},
			status:    map[string]api.VPAComponentStatus{"rs0": {}},
			expectCPU: "100m",
		},
		{
			name:      "Auto uses the committed value",
			vpa:       &api.VPASpec{Enabled: true, UpdateMode: api.VPAUpdateModeAuto},
			status:    map[string]api.VPAComponentStatus{"rs0": {Resources: committed}},
			expectCPU: "350m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{}
			cr.Spec.VPA = tt.vpa
			cr.Status.VPAStatus = tt.status

			got := cr.VPAResources("rs0", declared)

			cpu := got.Requests[corev1.ResourceCPU]
			assert.Equal(t, tt.expectCPU, cpu.String())
			// Limits are carried through in every case.
			assertResourceList(t, rl("4", "4Gi"), got.Limits)
		})
	}
}

func TestCommitVPAResourcesLeavesSpecAlone(t *testing.T) {
	r := vpaReaderWith(t)
	cr := vpaTestCR(true)
	cr.Spec.VPA.UpdateMode = api.VPAUpdateModeAuto
	declared := corev1.ResourceRequirements{
		Requests: rl("100m", "100M"),
		Limits:   rl("4", "4Gi"),
	}

	r.commitVPAResources(t.Context(), cr, "rs0", declared, rl("350m", "350Mi"), vpaBounds{})

	// The committed value lands in status...
	s := cr.Status.VPAStatus["rs0"]
	require.NotNil(t, s.Resources)
	assertResourceList(t, rl("350m", "350Mi"), s.Resources.Requests)
	require.NotNil(t, s.LastAppliedAt)

	// ...and the caller's declared spec is untouched.
	assertResourceList(t, rl("100m", "100M"), declared.Requests)
}

func TestCommitVPAResourcesComputesFromDeclaredBaseline(t *testing.T) {
	// Limit scaling must use the user's declared ratio every time rather than
	// compounding on the previous commit.
	r := vpaReaderWith(t)
	cr := vpaTestCR(true)
	cr.Spec.VPA.UpdateMode = api.VPAUpdateModeAuto
	cr.Spec.VPA.ControlledValues = api.VPAControlledValuesRequestsAndLimits

	declared := corev1.ResourceRequirements{
		Requests: rl("1", ""),
		Limits:   rl("2", ""),
	}

	r.commitVPAResources(t.Context(), cr, "rs0", declared, rl("2", ""), vpaBounds{})
	first := cr.Status.VPAStatus["rs0"].Resources.Limits.DeepCopy()

	r.commitVPAResources(t.Context(), cr, "rs0", declared, rl("2", ""), vpaBounds{})
	second := cr.Status.VPAStatus["rs0"].Resources.Limits

	assertResourceList(t, rl("4", ""), first)
	assertResourceList(t, first, second)
}
