package perconaservermongodb

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

// vpaGVK is the GroupVersionKind for VerticalPodAutoscaler objects.
// The operator never creates these — it only reads them.
var vpaGVK = schema.GroupVersionKind{
	Group:   "autoscaling.k8s.io",
	Version: "v1",
	Kind:    "VerticalPodAutoscaler",
}

// reconcileVPA reads VPA recommendations for all replsets and mongos, records them in
// status, and (when updateMode=Auto) patches CR resources and triggers SmartUpdate.
//
// The operator is recommendation-agnostic: any external tool (VPA Recommender, Goldilocks,
// etc.) that writes to a VerticalPodAutoscaler object is a valid recommendation source.
func (r *ReconcilePerconaServerMongoDB) reconcileVPA(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if cr.Spec.VPA == nil || !cr.Spec.VPA.Enabled {
		cr.Status.RemoveCondition(api.ConditionTypeVPAReady)
		return nil
	}

	log := logf.FromContext(ctx).WithName("VPA")

	// The operator only reads VerticalPodAutoscaler objects; it never installs the CRD.
	// Resolve the kind once per reconcile: without the CRD every per-component Get below
	// would fail with the same NoKindMatchError, which client.IgnoreNotFound does not
	// swallow, producing one logged error per component on every reconcile and a
	// rediscovery round trip per miss.
	if _, err := r.client.RESTMapper().RESTMapping(vpaGVK.GroupKind(), vpaGVK.Version); err != nil {
		if meta.IsNoMatchError(err) {
			log.Info("spec.vpa.enabled is set but the VerticalPodAutoscaler CRD is not installed, skipping VPA reconciliation")
			cr.Status.AddCondition(api.ClusterCondition{
				Status:  api.ConditionFalse,
				Type:    api.ConditionTypeVPAReady,
				Reason:  "VPACRDNotInstalled",
				Message: "spec.vpa.enabled is set but the VerticalPodAutoscaler CRD (autoscaling.k8s.io/v1) is not installed in the cluster",
			})
			return nil
		}
		return errors.Wrap(err, "resolve VerticalPodAutoscaler kind")
	}

	cr.Status.AddCondition(api.ClusterCondition{
		Status:  api.ConditionTrue,
		Type:    api.ConditionTypeVPAReady,
		Reason:  "VPACRDInstalled",
		Message: "operator is reading VerticalPodAutoscaler recommendations",
	})

	for _, rs := range cr.Spec.Replsets {
		if rs == nil {
			continue
		}

		// Main replset members.
		if rec := r.vpaRecommendationFor(ctx, cr, rs.Name, vpaObjectName(cr, rs.Name, rs.VPA),
			naming.ContainerMongod, effectiveWindow(cr.Spec.VPA, rs.VPA)); rec != nil {
			r.commitVPAResources(ctx, cr, rs.Name, rs.Resources, rec, effectiveBounds(cr.Spec.VPA, rs.VPA))
		}

		// Non-voting members — separate StatefulSet with container "mongod-nv".
		if rs.NonVoting.Enabled {
			if rec := r.vpaRecommendationFor(ctx, cr, rs.Name+"-nv",
				vpaObjectName(cr, rs.Name+"-nv", rs.NonVoting.VPA),
				naming.ContainerNonVoting, effectiveWindow(cr.Spec.VPA, rs.NonVoting.VPA)); rec != nil {
				r.commitVPAResources(ctx, cr, rs.Name+"-nv", rs.NonVoting.Resources, rec,
					effectiveBounds(cr.Spec.VPA, rs.NonVoting.VPA))
			}
		}

		// Hidden members — separate StatefulSet with container "mongod-hidden".
		if rs.Hidden.Enabled {
			if rec := r.vpaRecommendationFor(ctx, cr, rs.Name+"-hidden",
				vpaObjectName(cr, rs.Name+"-hidden", rs.Hidden.VPA),
				naming.ContainerHidden, effectiveWindow(cr.Spec.VPA, rs.Hidden.VPA)); rec != nil {
				r.commitVPAResources(ctx, cr, rs.Name+"-hidden", rs.Hidden.Resources, rec,
					effectiveBounds(cr.Spec.VPA, rs.Hidden.VPA))
			}
		}
	}

	// Config server replset — only when sharding is enabled.
	// spec.Replset("cfg") already resolves to spec.Sharding.ConfigsvrReplSet, so
	// applyVPAToReplset works without modification.
	if cr.Spec.Sharding.Enabled && cr.Spec.Sharding.ConfigsvrReplSet != nil {
		cfg := cr.Spec.Sharding.ConfigsvrReplSet
		if rec := r.vpaRecommendationFor(ctx, cr, cfg.Name, vpaObjectName(cr, cfg.Name, cfg.VPA),
			naming.ContainerMongod, effectiveWindow(cr.Spec.VPA, cfg.VPA)); rec != nil {
			r.commitVPAResources(ctx, cr, cfg.Name, cfg.Resources, rec, effectiveBounds(cr.Spec.VPA, cfg.VPA))
		}
	}

	// Mongos — only when sharding is enabled.
	if cr.Spec.Sharding.Enabled && cr.Spec.Sharding.Mongos != nil {
		mongos := cr.Spec.Sharding.Mongos
		if rec := r.vpaRecommendationFor(ctx, cr, "mongos", vpaObjectName(cr, "mongos", mongos.VPA),
			naming.ContainerMongos, effectiveWindow(cr.Spec.VPA, mongos.VPA)); rec != nil {
			r.commitVPAResources(ctx, cr, "mongos", mongos.Resources, rec,
				effectiveBounds(cr.Spec.VPA, mongos.VPA))
		}
	}

	return nil
}

// vpaRecommendationFor reads one component's recommendation, always records the
// outcome in status, and returns the recommendation only when it should be applied
// now. It returns nil when there is nothing to apply: no recommendation was
// available, updateMode is Off, or the stabilization window has not yet passed.
func (r *ReconcilePerconaServerMongoDB) vpaRecommendationFor(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	statusKey, vpaName, container string,
	window time.Duration,
) corev1.ResourceList {
	log := logf.FromContext(ctx).WithName("VPA")

	res, err := r.readVPARecommendation(ctx, cr.Namespace, vpaName, container)
	if err != nil {
		log.Error(err, "failed to read VPA recommendation", "vpa", vpaName, "component", statusKey)
		r.setVPAMessage(cr, statusKey, err.Error())
		return nil
	}

	if res.state != vpaStateOK {
		r.setVPAMessage(cr, statusKey, res.message(container))
		return nil
	}

	r.recordVPAStatus(cr, statusKey, res.rec)

	if cr.Spec.VPA.UpdateMode != api.VPAUpdateModeAuto {
		r.setVPAMessage(cr, statusKey, fmt.Sprintf(
			"recommendation recorded only, spec.vpa.updateMode is %s", cr.Spec.VPA.UpdateMode))
		return nil
	}
	if !stabilizationWindowPassed(cr, statusKey, window) {
		r.setVPAMessage(cr, statusKey, stabilizationMessage(cr, statusKey, window))
		return nil
	}

	r.setVPAMessage(cr, statusKey, "")
	return res.rec
}

// setVPAMessage records why a component is or is not applying recommendations,
// and is the single owner of that field: every path through vpaRecommendationFor
// sets it exactly once, passing "" when there is nothing to explain. Were it set
// in one place and cleared in another, the two would alternate and log on every
// reconcile. The message is logged only when it changes, so a steady state stays
// quiet.
func (r *ReconcilePerconaServerMongoDB) setVPAMessage(
	cr *api.PerconaServerMongoDB,
	statusKey string,
	message string,
) {
	if cr.Status.VPAStatus == nil {
		cr.Status.VPAStatus = make(map[string]api.VPAComponentStatus)
	}
	s := cr.Status.VPAStatus[statusKey]
	if s.Message != message {
		log := logf.Log.WithName("VPA")
		if message == "" {
			log.Info("VPA is applying recommendations for this component again",
				"component", statusKey)
		} else {
			log.Info("VPA is not applying recommendations for this component",
				"component", statusKey, "reason", message)
		}
	}
	s.Message = message
	cr.Status.VPAStatus[statusKey] = s
}

// stabilizationMessage says when the next apply becomes possible, so that a
// component sitting still inside its window explains itself rather than looking
// stalled.
func stabilizationMessage(
	cr *api.PerconaServerMongoDB,
	statusKey string,
	window time.Duration,
) string {
	s, ok := cr.Status.VPAStatus[statusKey]
	if !ok || s.LastAppliedAt == nil {
		return ""
	}
	return fmt.Sprintf(
		"recommendation recorded, next apply allowed after %s (stabilizationWindow %s)",
		s.LastAppliedAt.Add(window).UTC().Format(time.RFC3339), window,
	)
}

// vpaObjectName returns the VPA object name for a component.
// Uses the explicit objectName if configured; falls back to "<cluster>-<component>-vpa".
func vpaObjectName(cr *api.PerconaServerMongoDB, component string, spec *api.ComponentVPASpec) string {
	if spec != nil && spec.ObjectName != "" {
		return spec.ObjectName
	}
	return cr.Name + "-" + component + "-vpa"
}

// vpaBounds holds the effective min/max resource bounds for one component.
type vpaBounds struct {
	minAllowed corev1.ResourceList
	maxAllowed corev1.ResourceList
}

// effectiveBounds merges global and per-component VPA bounds.
// Per-component values take precedence over global defaults.
func effectiveBounds(global *api.VPASpec, component *api.ComponentVPASpec) vpaBounds {
	b := vpaBounds{
		minAllowed: global.MinAllowed,
		maxAllowed: global.MaxAllowed,
	}
	if component == nil {
		return b
	}
	if len(component.MinAllowed) > 0 {
		b.minAllowed = component.MinAllowed
	}
	if len(component.MaxAllowed) > 0 {
		b.maxAllowed = component.MaxAllowed
	}
	return b
}

// effectiveWindow returns the stabilization window for a component.
// Per-component setting overrides the global default.
func effectiveWindow(global *api.VPASpec, component *api.ComponentVPASpec) time.Duration {
	if component != nil && component.StabilizationWindow != nil {
		return component.StabilizationWindow.Duration
	}
	return global.StabilizationWindow.Duration
}

// vpaReadState explains the outcome of reading a recommendation, so that a
// component with no recommendation can say why rather than failing silently.
type vpaReadState string

const (
	// vpaStateOK — a recommendation for the requested container was read.
	vpaStateOK vpaReadState = "OK"

	// vpaStateObjectNotFound — the VPA object does not exist. Normal while an
	// external tool has yet to create it; otherwise usually a wrong objectName.
	vpaStateObjectNotFound vpaReadState = "ObjectNotFound"

	// vpaStateNoRecommendation — the object exists but carries no recommendation.
	// Either the recommender has not run yet, or it cannot resolve the object's
	// targetRef to a set of pods.
	vpaStateNoRecommendation vpaReadState = "NoRecommendation"

	// vpaStateContainerNotFound — the object carries recommendations, but none for
	// the container this component runs. Usually the object describes a different
	// workload.
	vpaStateContainerNotFound vpaReadState = "ContainerNotFound"
)

// vpaReadResult carries the recommendation together with enough context to explain
// its absence.
type vpaReadResult struct {
	rec     corev1.ResourceList
	state   vpaReadState
	vpaName string
	// containers lists the container names the object does carry, used to make
	// the ContainerNotFound message actionable.
	containers []string
}

// message renders the reason no recommendation is available, or "" when one is.
func (res vpaReadResult) message(container string) string {
	switch res.state {
	case vpaStateObjectNotFound:
		return fmt.Sprintf("VerticalPodAutoscaler %q not found", res.vpaName)
	case vpaStateNoRecommendation:
		return fmt.Sprintf(
			"VerticalPodAutoscaler %q exists but carries no recommendation; if it has just been "+
				"created the recommender may not have run yet, otherwise check that its targetRef "+
				"names the component's StatefulSet — a targetRef naming the PerconaServerMongoDB "+
				"resource cannot be resolved by the VPA recommender",
			res.vpaName)
	case vpaStateContainerNotFound:
		return fmt.Sprintf(
			"VerticalPodAutoscaler %q carries recommendations for %v but none for container %q",
			res.vpaName, res.containers, container)
	}
	return ""
}

// readVPARecommendation fetches the VPA object and extracts the target recommendation
// for the named container, reporting why when there is none.
func (r *ReconcilePerconaServerMongoDB) readVPARecommendation(
	ctx context.Context,
	namespace, vpaName, containerName string,
) (vpaReadResult, error) {
	res := vpaReadResult{vpaName: vpaName}

	vpa := &unstructured.Unstructured{}
	vpa.SetGroupVersionKind(vpaGVK)

	err := r.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: vpaName}, vpa)
	if err != nil {
		// VPA object not yet created by the external tool — normal during initial setup.
		if client.IgnoreNotFound(err) == nil {
			res.state = vpaStateObjectNotFound
			return res, nil
		}
		// The CRD can be uninstalled between the check in reconcileVPA and this read.
		// IgnoreNotFound does not cover that: a missing kind surfaces as NoKindMatchError,
		// which carries no NotFound status reason.
		if meta.IsNoMatchError(err) {
			res.state = vpaStateObjectNotFound
			return res, nil
		}
		return res, errors.Wrap(err, "get VPA object")
	}

	// Navigate: .status.recommendation.containerRecommendations[]
	recs, found, err := unstructured.NestedSlice(vpa.Object,
		"status", "recommendation", "containerRecommendations")
	if err != nil || !found || len(recs) == 0 {
		res.state = vpaStateNoRecommendation
		return res, nil
	}

	for _, item := range recs {
		rec, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name, _, _ := unstructured.NestedString(rec, "containerName")
		res.containers = append(res.containers, name)
		if name != containerName {
			continue
		}
		target, found, _ := unstructured.NestedMap(rec, "target")
		if !found {
			res.state = vpaStateNoRecommendation
			return res, nil
		}
		res.rec = parseResourceList(target)
		res.state = vpaStateOK
		return res, nil
	}

	res.state = vpaStateContainerNotFound
	return res, nil
}

// parseResourceList converts an unstructured map (from VPA status) to corev1.ResourceList.
func parseResourceList(m map[string]interface{}) corev1.ResourceList {
	rl := make(corev1.ResourceList, len(m))
	for k, v := range m {
		str, ok := v.(string)
		if !ok {
			continue
		}
		q, err := resource.ParseQuantity(str)
		if err != nil {
			continue
		}
		rl[corev1.ResourceName(k)] = q
	}
	return rl
}

// clampRecommendation enforces minAllowed/maxAllowed on a recommendation.
func clampRecommendation(rec corev1.ResourceList, bounds vpaBounds) corev1.ResourceList {
	result := rec.DeepCopy()
	for res, val := range result {
		if min, ok := bounds.minAllowed[res]; ok && val.Cmp(min) < 0 {
			result[res] = min.DeepCopy()
		}
		if max, ok := bounds.maxAllowed[res]; ok && val.Cmp(max) > 0 {
			result[res] = max.DeepCopy()
		}
	}
	return result
}

// recordVPAStatus writes the observed recommendation into cr.Status.VPAStatus.
func (r *ReconcilePerconaServerMongoDB) recordVPAStatus(
	cr *api.PerconaServerMongoDB,
	statusKey string,
	rec corev1.ResourceList,
) {
	if cr.Status.VPAStatus == nil {
		cr.Status.VPAStatus = make(map[string]api.VPAComponentStatus)
	}
	s := cr.Status.VPAStatus[statusKey]

	// Only record an observation that says something new. Writing the timestamp
	// on every reconcile would make the status differ every time, so the API
	// server could never collapse the update and every VPA-enabled cluster
	// would bump its resourceVersion forever. lastObservedAt therefore means
	// "the observed recommendation last changed", matching lastAppliedAt.
	changed := s.LastObservedAt == nil
	if cpu, ok := rec[corev1.ResourceCPU]; ok && !sameQuantityString(s.CPU, cpu) {
		s.CPU = cpu.String()
		changed = true
	}
	if mem, ok := rec[corev1.ResourceMemory]; ok && !sameQuantityString(s.Memory, mem) {
		s.Memory = mem.String()
		changed = true
	}
	if !changed {
		return
	}

	now := metav1.Now()
	s.LastObservedAt = &now
	cr.Status.VPAStatus[statusKey] = s
}

// sameQuantityString reports whether a quantity already recorded in status as a
// string describes the same amount as q. Comparing by value rather than by text
// keeps a recommender that switches units from registering as a change. An
// empty or unparseable stored value counts as different, so it gets rewritten.
func sameQuantityString(stored string, q resource.Quantity) bool {
	if stored == "" {
		return false
	}
	parsed, err := resource.ParseQuantity(stored)
	if err != nil {
		return false
	}
	return parsed.Cmp(q) == 0
}

// stabilizationWindowPassed returns true if enough time has elapsed since the last apply,
// or if no apply has occurred yet. A zero window always returns true.
func stabilizationWindowPassed(cr *api.PerconaServerMongoDB, statusKey string, window time.Duration) bool {
	if window == 0 {
		return true
	}
	if cr.Status.VPAStatus == nil {
		return true
	}
	s, ok := cr.Status.VPAStatus[statusKey]
	if !ok || s.LastAppliedAt == nil {
		return true
	}
	return time.Since(s.LastAppliedAt.Time) >= window
}

// commitVPAResources turns a recommendation into the component's effective
// resource requirements and records them in status. The StatefulSet is generated
// from that value on the next reconcile.
//
// Nothing is written to spec: it stays the user's declaration, which keeps it
// single-writer and safe for a GitOps controller to own. declared is always the
// spec baseline, so clamping and limit scaling are computed from what the user
// asked for rather than compounding on the previous commit.
func (r *ReconcilePerconaServerMongoDB) commitVPAResources(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	statusKey string,
	declared corev1.ResourceRequirements,
	rec corev1.ResourceList,
	bounds vpaBounds,
) {
	log := logf.FromContext(ctx).WithName("VPA")
	clamped := clampRecommendation(rec, bounds)

	limits := declared.Limits
	if cr.Spec.VPA.ControlledValues == api.VPAControlledValuesRequestsAndLimits {
		limits = ratioScaledLimits(declared, clamped)
	} else {
		// RequestsOnly: cap new requests at the declared limits so Kubernetes never
		// rejects the StatefulSet update with "requests must be <= limits".
		clamped = capRequestsAtLimits(clamped, declared.Limits)
	}

	// Resources the recommendation does not mention keep their declared values.
	requests := declared.Requests.DeepCopy()
	if requests == nil {
		requests = make(corev1.ResourceList)
	}
	for res, val := range clamped {
		requests[res] = val
	}

	effective := corev1.ResourceRequirements{Requests: requests}
	if limits != nil {
		effective.Limits = limits.DeepCopy()
	}

	if cr.Status.VPAStatus == nil {
		cr.Status.VPAStatus = make(map[string]api.VPAComponentStatus)
	}
	s := cr.Status.VPAStatus[statusKey]

	// Only a change is a commit. The recommender holds a value steady for long
	// stretches, so without this the component would be re-committed once per
	// stabilization window forever: status churns, the log fills, and
	// lastAppliedAt comes to mean "last reconciled" instead of "last changed".
	if s.Resources != nil && sameResourceRequirements(*s.Resources, effective) {
		return
	}

	now := metav1.Now()
	s.Resources = &effective
	s.LastAppliedAt = &now
	cr.Status.VPAStatus[statusKey] = s

	log.Info("committed VPA resources",
		"component", statusKey,
		"cpu", requests.Cpu().String(),
		"memory", requests.Memory().String(),
	)
}

// sameResourceRequirements reports whether two requirements describe the same
// resources. Quantities are compared by value, not by string form, so a commit
// of 1Gi is not treated as a change when 1024Mi is already recorded.
func sameResourceRequirements(a, b corev1.ResourceRequirements) bool {
	return quantitiesEqual(a.Requests, b.Requests) && quantitiesEqual(a.Limits, b.Limits)
}

// capRequestsAtLimits ensures that no request exceeds the corresponding limit.
// This is a safety guard for RequestsOnly mode: if the recommendation (after
// min/max clamping) would push requests above the existing limits, we cap
// the requests so Kubernetes does not reject the StatefulSet update.
// Resources with no existing limit are left untouched.
func capRequestsAtLimits(requests corev1.ResourceList, limits corev1.ResourceList) corev1.ResourceList {
	if len(limits) == 0 {
		return requests
	}
	result := requests.DeepCopy()
	for res, req := range result {
		if lim, ok := limits[res]; ok && req.Cmp(lim) > 0 {
			result[res] = lim.DeepCopy()
		}
	}
	return result
}

// ratioScaledLimits returns new limits by preserving the original limit/request ratio.
// For each resource: newLimit = newRequest * (oldLimit / oldRequest).
// Resources with no existing limit or a zero request are left unchanged.
func ratioScaledLimits(orig corev1.ResourceRequirements, newRequests corev1.ResourceList) corev1.ResourceList {
	if orig.Limits == nil {
		return nil
	}
	newLimits := orig.Limits.DeepCopy()
	for res, newReq := range newRequests {
		oldReq, hasReq := orig.Requests[res]
		oldLim, hasLim := orig.Limits[res]
		if !hasReq || !hasLim || oldReq.IsZero() {
			continue
		}
		ratio := float64(oldLim.MilliValue()) / float64(oldReq.MilliValue())
		newLimMilli := int64(float64(newReq.MilliValue()) * ratio)
		newLimits[res] = *resource.NewMilliQuantity(newLimMilli, resource.DecimalSI)
	}
	return newLimits
}
