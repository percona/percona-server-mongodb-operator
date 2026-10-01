package perconaservermongodb

import (
	"context"
	"encoding/json"
	"time"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	k8sversion "k8s.io/apimachinery/pkg/util/version"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

const inPlaceResizeTimeout = 2 * time.Minute

type memoryChange int

const (
	memoryUnchanged memoryChange = iota
	memoryGrow
	memoryShrink
)

type inPlaceResize struct {
	container string
	target    corev1.ResourceRequirements
	// memory is only set for mongod containers, whose WiredTiger cache must follow the limit.
	memory  memoryChange
	cacheGB float64
}

// managedContainer returns the VPA-managed container of a pod component, or ""
// for components VPA does not manage.
func managedContainer(component string) string {
	switch component {
	case naming.ComponentMongod, naming.ComponentConfigSrv:
		return naming.ContainerMongod
	case naming.ComponentNonVoting:
		return naming.ContainerNonVoting
	case naming.ComponentHidden:
		return naming.ContainerHidden
	case naming.ComponentMongos:
		return naming.ContainerMongos
	}
	return ""
}

func findContainer(containers []corev1.Container, name string) *corev1.Container {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) inPlaceResizeSupported() bool {
	if r.serverVersion == nil {
		return false
	}
	v, err := k8sversion.ParseGeneric(r.serverVersion.Info.GitVersion)
	if err != nil {
		return false
	}
	return v.AtLeast(k8sversion.MajorMinor(1, 33))
}

func (r *ReconcilePerconaServerMongoDB) revisionTemplate(ctx context.Context, namespace, name string) (corev1.PodTemplateSpec, error) {
	reader := r.apiReader
	if reader == nil {
		reader = r.client
	}

	// Read uncached: the cache may not have the revision the StatefulSet status just reported.
	rev := new(appsv1.ControllerRevision)
	if err := reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, rev); err != nil {
		return corev1.PodTemplateSpec{}, err
	}

	// StatefulSet revisions store the pod template as {"spec":{"template":{...}}}.
	var data struct {
		Spec struct {
			Template corev1.PodTemplateSpec `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(rev.Data.Raw, &data); err != nil {
		return corev1.PodTemplateSpec{}, errors.Wrapf(err, "decode controller revision %s", name)
	}
	return data.Spec.Template, nil
}

// onlyResourcesDiffer reports whether two pod templates differ in nothing but
// the resources of the named container. Init containers copy those resources
// and have already run, so their resources are ignored too.
func onlyResourcesDiffer(a, b corev1.PodTemplateSpec, container string) bool {
	a, b = *a.DeepCopy(), *b.DeepCopy()
	for _, t := range []*corev1.PodTemplateSpec{&a, &b} {
		if c := findContainer(t.Spec.Containers, container); c != nil {
			c.Resources = corev1.ResourceRequirements{}
		}
		for i := range t.Spec.InitContainers {
			t.Spec.InitContainers[i].Resources = corev1.ResourceRequirements{}
		}
	}
	return equality.Semantic.DeepEqual(a, b)
}

// planInPlaceResize decides whether the pod can be moved to updateRevision by
// resizing it in place. It returns nil and the reason when it cannot.
func (r *ReconcilePerconaServerMongoDB) planInPlaceResize(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	replset *api.ReplsetSpec,
	updateRevision string,
	pod *corev1.Pod,
) (*inPlaceResize, string, error) {
	if !cr.VPAInPlaceResizeEnabled() {
		return nil, "in-place resize is disabled", nil
	}
	if !r.inPlaceResizeSupported() {
		return nil, "Kubernetes server is older than 1.33", nil
	}

	container := managedContainer(pod.Labels[naming.LabelKubernetesComponent])
	if container == "" {
		return nil, "component is not managed by VPA", nil
	}

	cur, err := r.revisionTemplate(ctx, pod.Namespace, pod.Labels["controller-revision-hash"])
	if k8sErrors.IsNotFound(err) {
		return nil, "pod's controller revision not found", nil
	}
	if err != nil {
		return nil, "", errors.Wrap(err, "get pod revision")
	}
	upd, err := r.revisionTemplate(ctx, pod.Namespace, updateRevision)
	if k8sErrors.IsNotFound(err) {
		return nil, "update revision not found", nil
	}
	if err != nil {
		return nil, "", errors.Wrap(err, "get update revision")
	}

	if !onlyResourcesDiffer(cur, upd, container) {
		return nil, "update changes more than the container's resources", nil
	}

	curC := findContainer(cur.Spec.Containers, container)
	updC := findContainer(upd.Spec.Containers, container)
	if curC == nil || updC == nil || findContainer(pod.Spec.Containers, container) == nil {
		return nil, "container not found", nil
	}

	plan := &inPlaceResize{container: container, target: updC.Resources}
	if container == naming.ContainerMongos {
		return plan, "", nil
	}

	// Compare against the revision the pod was created from: after an earlier,
	// interrupted attempt pod.spec may already hold the target.
	switch curC.Resources.Limits.Memory().Cmp(*updC.Resources.Limits.Memory()) {
	case -1:
		plan.memory = memoryGrow
	case 1:
		plan.memory = memoryShrink
	}
	if plan.memory == memoryUnchanged {
		return plan, "", nil
	}

	if replset == nil || replset.Storage == nil || replset.Storage.Engine != api.StorageEngineWiredTiger {
		return nil, "memory limit change needs a restart for this storage engine", nil
	}
	plan.cacheGB = psmdb.WiredTigerCacheSizeGB(updC.Resources.Limits, replset.Storage.WiredTiger.EngineConfig.CacheSizeRatio.Float64())

	return plan, "", nil
}

// tryInPlaceResize moves the pod to updateRevision by resizing it in place. It
// returns false when the pod should be recreated instead. It keeps no state of
// its own, so an interrupted attempt continues on the next call.
func (r *ReconcilePerconaServerMongoDB) tryInPlaceResize(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	replset *api.ReplsetSpec,
	updateRevision string,
	pod *corev1.Pod,
) (bool, error) {
	log := logf.FromContext(ctx).WithName("InPlaceResize").WithValues("pod", pod.Name)

	plan, reason, err := r.planInPlaceResize(ctx, cr, replset, updateRevision, pod)
	if err != nil {
		return false, errors.Wrap(err, "plan in-place resize")
	}
	if plan == nil {
		if cr.VPAInPlaceResizeEnabled() {
			log.Info("recreating pod instead of resizing in place", "reason", reason)
		}
		return false, nil
	}

	// Shrink the cache before the limit so mongod's usage fits under it.
	if plan.memory == memoryShrink {
		if err := r.setPodCacheSize(ctx, cr, replset, pod, plan.cacheGB); err != nil {
			log.Info("recreating pod: failed to shrink WiredTiger cache", "error", err.Error())
			return false, nil
		}
	}

	if err := r.resizePod(ctx, pod, plan); err != nil {
		log.Info("recreating pod: resize rejected", "error", err.Error())
		return false, nil
	}

	if reason := r.waitPodResized(ctx, pod, plan); reason != "" {
		log.Info("recreating pod: resize did not complete", "reason", reason)
		return false, nil
	}

	if plan.memory == memoryGrow {
		if err := r.setPodCacheSize(ctx, cr, replset, pod, plan.cacheGB); err != nil {
			// More memory with the old cache is safe; a restart would defeat the purpose.
			log.Error(err, "failed to grow WiredTiger cache, keeping the previous size until the pod restarts")
		}
	}

	orig := pod.DeepCopy()
	pod.Labels["controller-revision-hash"] = updateRevision
	if err := r.client.Patch(ctx, pod, client.MergeFrom(orig)); err != nil {
		return false, errors.Wrap(err, "relabel pod")
	}

	log.Info("pod resized in place",
		"container", plan.container,
		"cpu", plan.target.Requests.Cpu().String(),
		"memory", plan.target.Requests.Memory().String())
	return true, nil
}

func (r *ReconcilePerconaServerMongoDB) resizePod(ctx context.Context, pod *corev1.Pod, plan *inPlaceResize) error {
	c := findContainer(pod.Spec.Containers, plan.container)
	if resourcesApplied(c.Resources, plan.target) {
		return nil
	}

	orig := pod.DeepCopy()
	c.Resources = plan.target
	return r.client.SubResource("resize").Patch(ctx, pod, client.StrategicMergeFrom(orig))
}

// waitPodResized polls the pod until the kubelet has applied plan.target. It
// returns "" on success, or why the resize will not complete.
func (r *ReconcilePerconaServerMongoDB) waitPodResized(ctx context.Context, pod *corev1.Pod, plan *inPlaceResize) string {
	deadline := time.Now().Add(inPlaceResizeTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)

		if err := r.client.Get(ctx, client.ObjectKeyFromObject(pod), pod); err != nil {
			return "get pod: " + err.Error()
		}

		inProgress := false
		for _, cond := range pod.Status.Conditions {
			switch cond.Type {
			case corev1.PodResizePending:
				if cond.Reason == corev1.PodReasonInfeasible {
					return "resize is infeasible: " + cond.Message
				}
				inProgress = true
			case corev1.PodResizeInProgress:
				if cond.Reason == corev1.PodReasonError {
					return "resize failed: " + cond.Message
				}
				inProgress = true
			}
		}
		if inProgress {
			continue
		}

		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == plan.container && cs.Resources != nil && resourcesApplied(*cs.Resources, plan.target) {
				return ""
			}
		}
	}
	return "timed out after " + inPlaceResizeTimeout.String()
}

// resourcesApplied reports whether actual (pod spec or container status) holds
// target. The API server defaults missing requests to limits, and the kubelet
// reports memory in whole bytes, so both are normalized before comparing.
func resourcesApplied(actual, target corev1.ResourceRequirements) bool {
	actual, target = normalizeResources(actual), normalizeResources(target)
	return quantitiesEqual(actual.Requests, target.Requests) && quantitiesEqual(actual.Limits, target.Limits)
}

func normalizeResources(r corev1.ResourceRequirements) corev1.ResourceRequirements {
	r = *r.DeepCopy()
	for name, limit := range r.Limits {
		if _, ok := r.Requests[name]; !ok {
			if r.Requests == nil {
				r.Requests = corev1.ResourceList{}
			}
			r.Requests[name] = limit
		}
	}
	return r
}

func quantitiesEqual(a, b corev1.ResourceList) bool {
	if len(a) != len(b) {
		return false
	}
	for name, qa := range a {
		qb, ok := b[name]
		if !ok {
			return false
		}
		if name == corev1.ResourceCPU {
			if qa.MilliValue() != qb.MilliValue() {
				return false
			}
		} else if qa.Value() != qb.Value() {
			return false
		}
	}
	return true
}

func (r *ReconcilePerconaServerMongoDB) setPodCacheSize(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	replset *api.ReplsetSpec,
	pod *corev1.Pod,
	sizeGB float64,
) error {
	c, err := r.standaloneClientWithRole(ctx, cr, replset, api.RoleClusterAdmin, *pod)
	if err != nil {
		return errors.Wrap(err, "create standalone client")
	}
	defer func() {
		if err := c.Disconnect(ctx); err != nil {
			logf.FromContext(ctx).Error(err, "failed to close connection")
		}
	}()

	return mongo.SetWiredTigerCacheSize(ctx, c, sizeGB)
}
