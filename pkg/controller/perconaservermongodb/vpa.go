package perconaservermongodb

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	vpav1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

// vpaGVK is the GroupVersionKind for VerticalPodAutoscaler objects.
// The operator never creates these — it only reads them.
var vpaGVK = vpav1.SchemeGroupVersion.WithKind("VerticalPodAutoscaler")

// reconcileVPARecommendations reads VPA recommendations for all replsets and mongos, records them in
// status, and (when updateMode=Auto) patches CR resources and triggers SmartUpdate.
//
// The operator is recommendation-agnostic: any external tool (VPA Recommender, Goldilocks,
// etc.) that writes to a VerticalPodAutoscaler object is a valid recommendation source.
func (r *ReconcilePerconaServerMongoDB) reconcileVPARecommendations(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if cr.Spec.VPA == nil || !cr.Spec.VPA.Enabled {
		cr.Status.RemoveCondition(api.ConditionTypeVPAReady)
		return nil
	}

	if ok, err := util.GroupVersionKindExists(r.client, vpaGVK); err != nil {
		return errors.Wrap(err, "check autoscaling API discovery")
	} else if !ok {
		cr.Status.AddCondition(api.ClusterCondition{
			Status:  api.ConditionFalse,
			Type:    api.ConditionTypeVPAReady,
			Reason:  "APINotInstalled",
			Message: "spec.vpa.enabled is set but the autoscaling.k8s.io/v1.VerticalPodAutoscaler is not installed in the cluster",
		})
		return nil
	}

	cr.Status.AddCondition(api.ClusterCondition{
		Status:  api.ConditionTrue,
		Type:    api.ConditionTypeVPAReady,
		Reason:  "CRDInstalled",
		Message: "reading VerticalPodAutoscaler recommendations",
	})

	for _, rs := range cr.Spec.Replsets {
		if rs == nil {
			continue
		}

		if err := r.reconcileComponentVPA(ctx, cr, rs.Name, naming.ComponentMongod, rs.Resources, rs.VPA); err != nil {
			return errors.Wrapf(err, "failed to reconcile VPA for component '%s' in replset '%s'", naming.ComponentMongod, rs.Name)
		}

		if rs.NonVoting.Enabled {
			if err := r.reconcileComponentVPA(ctx, cr, rs.Name, naming.ComponentNonVoting, rs.NonVoting.Resources, rs.NonVoting.VPA); err != nil {
				return errors.Wrapf(err, "failed to reconcile VPA for component '%s' in replset '%s'", naming.ComponentNonVoting, rs.Name)
			}
		}

		if rs.Hidden.Enabled {
			if err := r.reconcileComponentVPA(ctx, cr, rs.Name, naming.ComponentHidden,
				rs.Hidden.Resources, rs.Hidden.VPA); err != nil {
				return errors.Wrapf(err, "failed to reconcile VPA for component '%s' in replset '%s'", naming.ComponentHidden, rs.Name)
			}
		}
	}

	if cr.Spec.Sharding.Enabled && cr.Spec.Sharding.ConfigsvrReplSet != nil {
		cfg := cr.Spec.Sharding.ConfigsvrReplSet
		if err := r.reconcileComponentVPA(ctx, cr, cfg.Name, naming.ComponentConfigSrv, cfg.Resources, cfg.VPA); err != nil {
			return errors.Wrapf(err, "failed to reconcile VPA for component '%s' in replset '%s'", naming.ComponentConfigSrv, cfg.Name)
		}
	}

	if cr.Spec.Sharding.Enabled && cr.Spec.Sharding.Mongos != nil {
		mongos := cr.Spec.Sharding.Mongos
		if err := r.reconcileComponentVPA(ctx, cr, "", naming.ComponentMongos, mongos.Resources, mongos.VPA); err != nil {
			return errors.Wrapf(err, "failed to reconcile VPA for component '%s'", naming.ComponentMongos)
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileComponentVPA(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	rsName, component string,
	declared corev1.ResourceRequirements,
	vpa *api.ComponentVPASpec,
) error {
	container := managedContainer(component)
	if container == "" {
		return nil
	}

	statusKey := naming.VPAStatusKey(rsName, component)
	rec, err := r.vpaRecommendationFor(ctx, cr, statusKey, container, vpa)
	if err != nil {
		return err
	}
	if rec == nil {
		return nil
	}
	r.commitVPAResources(ctx, cr, statusKey, declared, rec, effectiveBounds(cr.Spec.VPA, vpa))
	return nil
}

// vpaRecommendationFor reads one component's recommendation, always records the
// outcome in status, and returns the recommendation the component should be sized
// from, or nil when there is none: no recommendation was available, or updateMode
// is Off.
//
// The stabilization window gates only whether a new recommendation is adopted.
// While it is open this returns the one already in status, so the caller still
// recomputes the effective resources from the current spec on every reconcile and
// an edit to spec.<component>.resources is never held back by the window.
//
// vpa is the component's own VPA block, which together with statusKey is all that
// the object name and the stabilization window are derived from, so callers pass
// the block rather than deriving both themselves.
func (r *ReconcilePerconaServerMongoDB) vpaRecommendationFor(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	statusKey, container string,
	vpa *api.ComponentVPASpec,
) (corev1.ResourceList, error) {
	log := logf.FromContext(ctx).WithName("VPA")

	vpaName := naming.VPAName(cr, statusKey, vpa)
	window := effectiveWindow(cr.Spec.VPA, vpa)

	res, err := r.readVPARecommendation(ctx, cr.Namespace, vpaName, container)
	if err != nil {
		log.Error(err, "failed to read VPA recommendation", "vpa", vpaName, "component", statusKey)
		r.setVPAMessage(cr, statusKey, err.Error())
		return nil, nil
	}

	// Under UpdateModeAuto, if a recommendation was previously applied but the VPA is now missing,
	// we should return an error here for the user to handle. The operator cannot assume anything about
	// what the new resources must be.
	if lastApplied := cr.Status.VPAStatus[statusKey].Applied; lastApplied != nil &&
		res.state == vpaStateObjectNotFound &&
		cr.Spec.VPA.UpdateMode == api.VPAUpdateModeAuto {
		r.setVPAMessage(cr, statusKey, fmt.Sprintf(
			"VerticalPodAutoscaler %q not found; this component is still sized from the last "+
				"applied recommendation and reconciliation is stopped. Recreate the object, or set "+
				"spec.vpa.updateMode to Off to return to the declared resources",
			vpaName))
		return nil, errors.Errorf("VerticalPodAutoscaler %q not found", vpaName)
	}

	if res.state != vpaStateOK {
		r.setVPAMessage(cr, statusKey, res.message(container))
		return nil, nil
	}

	// Off observes without applying, so every observation is recorded: there
	// is no adoption to rate-limit and status is the only output.
	if cr.Spec.VPA.UpdateMode == api.VPAUpdateModeOff {
		r.recordVPAStatus(cr, statusKey, res.rec)
		r.setVPAMessage(cr, statusKey, fmt.Sprintf(
			"recommendation recorded only, spec.vpa.updateMode is %s", cr.Spec.VPA.UpdateMode))
		return nil, nil
	}

	// A recommendation that has not moved needs no decision regarding the window
	accepted := cr.Status.VPAStatus[statusKey].Recommended
	if quantitiesEqual(accepted, res.rec) {
		r.setVPAMessage(cr, statusKey, "")
		return accepted, nil
	}

	// New recommendations to be accepted only after the stabilization window has passed.
	if !stabilizationWindowPassed(cr, statusKey, window) {
		r.setVPAMessage(cr, statusKey, stabilizationMessage(cr, statusKey, res.rec, window))
		return accepted, nil
	}

	r.recordVPAStatus(cr, statusKey, res.rec)
	r.setVPAMessage(cr, statusKey, "")
	return res.rec, nil
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

// stabilizationMessage names the recommendation waiting to be adopted and when
// that becomes possible, so that a component sitting still inside its window
// explains itself rather than looking stalled. Naming the pending value is what
// makes the deferred change visible in status rather than only the fact of it.
func stabilizationMessage(
	cr *api.PerconaServerMongoDB,
	statusKey string,
	pending corev1.ResourceList,
	window time.Duration,
) string {
	s, ok := cr.Status.VPAStatus[statusKey]
	if !ok || s.LastObservedAt == nil {
		return ""
	}
	return fmt.Sprintf(
		"new recommendation cpu=%s memory=%s, next adoption allowed after %s (stabilizationWindow %s)",
		pending.Cpu().String(), pending.Memory().String(),
		s.LastObservedAt.Add(window).UTC().Format(time.RFC3339), window,
	)
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

	// vpaStateObjectNotFound — the VPA object does not exist. Either nobody has
	// written one for this component, or objectName names one that is not there.
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

	vpa := new(vpav1.VerticalPodAutoscaler)

	err := r.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: vpaName}, vpa)
	if err != nil {
		if k8serrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			res.state = vpaStateObjectNotFound
			return res, nil
		}
		return res, errors.Wrap(err, "get VPA object")
	}

	if vpa.Status.Recommendation == nil || len(vpa.Status.Recommendation.ContainerRecommendations) == 0 {
		res.state = vpaStateNoRecommendation
		return res, nil
	}

	for _, rec := range vpa.Status.Recommendation.ContainerRecommendations {
		res.containers = append(res.containers, rec.ContainerName)
		if rec.ContainerName != containerName {
			continue
		}
		if len(rec.Target) == 0 {
			res.state = vpaStateNoRecommendation
			return res, nil
		}

		rec := rec.Target.DeepCopy()
		for res := range rec {
			// we only care about CPU and memory
			if res != corev1.ResourceCPU && res != corev1.ResourceMemory {
				delete(rec, res)
			}
		}
		res.rec = rec
		res.state = vpaStateOK
		return res, nil
	}

	res.state = vpaStateContainerNotFound
	return res, nil
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

// recordVPAStatus writes a recommendation into cr.Status.VPAStatus together with
// the time it was read. Under Auto the caller only reaches this once the
// stabilization window allows adoption, so Recommended holds the recommendation
// currently in force and lastObservedAt doubles as the clock the window is
// measured from. Under Off nothing is applied, so every observation is recorded.
func (r *ReconcilePerconaServerMongoDB) recordVPAStatus(
	cr *api.PerconaServerMongoDB,
	statusKey string,
	rec corev1.ResourceList,
) {
	if cr.Status.VPAStatus == nil {
		cr.Status.VPAStatus = make(map[string]api.VPAComponentStatus)
	}
	s := cr.Status.VPAStatus[statusKey]

	// Only record an observation that says something new. quantitiesEqual compares
	// by value, so a recommender that switches units does not register as a change.
	if s.Recommended != nil && quantitiesEqual(s.Recommended, rec) {
		return
	}

	now := metav1.Now()
	s.Recommended = rec.DeepCopy()
	s.LastObservedAt = &now
	cr.Status.VPAStatus[statusKey] = s
}

// stabilizationWindowPassed returns true if enough time has elapsed since the
// recommendation in status was adopted, or if none has been adopted yet. A zero
// window always returns true.
//
// It measures from lastObservedAt, not lastAppliedAt: the window limits how often
// a new recommendation may be adopted, and lastObservedAt is when the one in force
// was taken. Measuring from the last apply would also block applies caused by the
// user editing spec.<component>.resources, which the window has no business over.
func stabilizationWindowPassed(cr *api.PerconaServerMongoDB, statusKey string, window time.Duration) bool {
	if window == 0 {
		return true
	}
	if cr.Status.VPAStatus == nil {
		return true
	}
	s, ok := cr.Status.VPAStatus[statusKey]
	if !ok || s.LastObservedAt == nil {
		return true
	}
	return time.Since(s.LastObservedAt.Time) >= window
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
	maps.Copy(requests, clamped)

	effective := corev1.ResourceRequirements{Requests: requests}
	if limits != nil {
		effective.Limits = limits.DeepCopy()
	}

	if cr.Status.VPAStatus == nil {
		cr.Status.VPAStatus = make(map[string]api.VPAComponentStatus)
	}
	s := cr.Status.VPAStatus[statusKey]

	// apply only when changed
	if s.Applied != nil &&
		quantitiesEqual(s.Applied.Limits, effective.Limits) &&
		quantitiesEqual(s.Applied.Requests, effective.Requests) {
		return
	}

	now := metav1.Now()
	s.Applied = &effective
	s.LastAppliedAt = &now
	cr.Status.VPAStatus[statusKey] = s

	log.Info("committed VPA resources",
		"component", statusKey,
		"cpuRequest", requests.Cpu().String(),
		"memoryRequest", requests.Memory().String(),
		"cpuLimits", limits.Cpu().String(),
		"memoryLimits", limits.Memory().String(),
	)
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
