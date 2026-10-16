package perconaservermongodb

import (
	"context"
	"time"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
)

// storageWriter persists a new requested size for the volume being autoscaled.
// A member group's size lives under its own entry in the CR and the mongos log
// size under sharding.mongos, so each caller brings the write that knows where
// its own size belongs.
type storageWriter func(ctx context.Context, size resource.Quantity) error

// volumeMount returns the container that mounts claimName and where it mounts
// it. Sidecars mount these volumes too — fluentbit with log collection on, and
// the backup agent for mongod-data — but the container running mongod or mongos
// is built first, so the first match is the one that owns the volume.
func volumeMount(sts *appsv1.StatefulSet, claimName string) (container, mountPath string, ok bool) {
	for _, c := range sts.Spec.Template.Spec.Containers {
		for _, m := range c.VolumeMounts {
			if m.Name == claimName {
				return c.Name, m.MountPath, true
			}
		}
	}

	return "", "", false
}

// reconcileGroupStorageAutoscaling autoscales the data volume of one member
// group. Every group owns a separate claim spec in the CR, so the size has to
// be written back through the group's source rather than to the replset.
func (r *ReconcilePerconaServerMongoDB) reconcileGroupStorageAutoscaling(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	sts *appsv1.StatefulSet,
	group membergroup.Group,
) error {
	if !group.DataBearing {
		return nil
	}

	var pvcSpec *api.PVCSpec
	if group.VolumeSpec != nil {
		pvcSpec = &group.VolumeSpec.PersistentVolumeClaim
	}

	ctx = logf.IntoContext(ctx, logf.FromContext(ctx).WithValues("group", group.Name))

	return r.reconcileStorageAutoscaling(ctx, cr, sts, config.MongodDataVolClaimName, pvcSpec,
		group.Labels, r.groupStorageWriter(cr, group))
}

func (r *ReconcilePerconaServerMongoDB) groupStorageWriter(
	cr *api.PerconaServerMongoDB,
	group membergroup.Group,
) storageWriter {
	return func(ctx context.Context, size resource.Quantity) error {
		return r.writeGroupStorageRequest(ctx, cr, group.Source, size)
	}
}

// mongosLogStorageWriter writes the mongos log volume's requested size. mongos
// is not a member group, so its size lives under sharding.mongos instead.
func (r *ReconcilePerconaServerMongoDB) mongosLogStorageWriter(
	cr *api.PerconaServerMongoDB,
) storageWriter {
	return func(ctx context.Context, size resource.Quantity) error {
		return r.writeMongosLogStorageRequest(ctx, cr, size)
	}
}

// reconcileStorageAutoscaling checks PVC disk usage and triggers resize if needed
func (r *ReconcilePerconaServerMongoDB) reconcileStorageAutoscaling(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	sts *appsv1.StatefulSet,
	claimName string,
	pvcSpec *api.PVCSpec,
	ls map[string]string,
	write storageWriter,
) error {
	log := logf.FromContext(ctx).WithName("StorageAutoscaling").
		WithValues("statefulset", sts.Name)

	autoscalingSpec := cr.Spec.StorageAutoscaling()
	if autoscalingSpec == nil || !autoscalingSpec.Enabled {
		return nil
	}

	if cr.Spec.IsExternalVolumeAutoscalingEnabled() {
		log.V(1).Info("skipping storage autoscaling: external autoscaling is enabled")
		return nil
	}

	if !cr.Spec.IsVolumeExpansionEnabled() {
		log.V(1).Info("skipping storage autoscaling: volume expansion is disabled")
		return nil
	}

	if pvcSpec == nil || pvcSpec.PersistentVolumeClaimSpec == nil {
		log.V(1).Info("skipping storage autoscaling: not using PVC")
		return nil
	}

	if _, ok := sts.Annotations[api.AnnotationPVCResizeInProgress]; ok {
		log.V(1).Info("PVC resize already in progress")
		return nil
	}

	container, mountPath, ok := volumeMount(sts, claimName)
	if !ok {
		log.V(1).Info("skipping storage autoscaling: no container mounts the volume",
			"claim", claimName)
		return nil
	}

	pvcList := &corev1.PersistentVolumeClaimList{}
	err := r.client.List(ctx, pvcList, &client.ListOptions{
		Namespace:     cr.Namespace,
		LabelSelector: labels.SelectorFromSet(ls),
	})
	if err != nil {
		return errors.Wrap(err, "list PVCs for autoscaling")
	}

	podList := &corev1.PodList{}
	err = r.client.List(ctx, podList, &client.ListOptions{
		Namespace:     cr.Namespace,
		LabelSelector: labels.SelectorFromSet(ls),
	})
	if err != nil {
		return errors.Wrap(err, "list pods for autoscaling")
	}

	for _, pvc := range pvcList.Items {
		podName, ok := pvcPodName(claimName, pvc.Name, sts)
		if !ok {
			continue
		}

		pod := findPodByName(podList, podName)
		if pod == nil {
			log.V(1).Info("pod not found for PVC", "pvc", pvc.Name, "pod", podName)
			continue
		}

		if err := r.checkAndResizePVC(ctx, cr, &pvc, pod, pvcSpec, container, mountPath, write); err != nil {
			log.Error(err, "failed to check/resize PVC", "pvc", pvc.Name)
			r.updateAutoscalingStatus(ctx, cr, pvc.Name, nil, err)
		}
	}

	return nil
}

// checkAndResizePVC checks a single PVC and triggers resize if needed
func (r *ReconcilePerconaServerMongoDB) checkAndResizePVC(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	pvc *corev1.PersistentVolumeClaim,
	pod *corev1.Pod,
	pvcSpec *api.PVCSpec,
	container string,
	mountPath string,
	write storageWriter,
) error {
	log := logf.FromContext(ctx).WithName("StorageAutoscaling").WithValues("pvc", pvc.Name)

	if !isContainerAndPodRunning(*pod, container) {
		log.V(1).Info("skipping PVC metrics check: container and pod not running",
			"phase", pod.Status.Phase, "container", container)
		return nil
	}

	usage, err := r.getPVCUsageFromMetrics(ctx, pod, pvc.Name, container, mountPath)
	if err != nil {
		return errors.Wrap(err, "get PVC usage from metrics")
	}

	r.updateAutoscalingStatus(ctx, cr, pvc.Name, usage, nil)

	if !r.shouldTriggerResize(ctx, cr, pvc, usage) {
		return nil
	}

	newSize := r.calculateNewSize(cr, pvc)

	// newSize is one step over the capacity the PVC has, so overwriting a bigger
	// request already in the spec would downgrade it. The resize path runs right
	// after this and applies that request, which covers the usage anyway.
	if requested, ok := pvcSpec.Resources.Requests[corev1.ResourceStorage]; ok && requested.Cmp(newSize) >= 0 {
		log.V(1).Info("skipping resize: a larger size is already requested",
			"requested", requested.String(), "autoscaledTo", newSize.String())
		return nil
	}

	log.Info("triggering storage autoscaling",
		"currentSize", pvc.Status.Capacity.Storage().String(),
		"newSize", newSize.String(),
		"usagePercent", usage.UsagePercent,
		"threshold", cr.Spec.StorageAutoscaling().TriggerThresholdPercent)

	return r.triggerResize(ctx, pvc, newSize, pvcSpec, write)
}

// shouldTriggerResize determines if a PVC should be resized
func (r *ReconcilePerconaServerMongoDB) shouldTriggerResize(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	pvc *corev1.PersistentVolumeClaim,
	usage *PVCUsage,
) bool {
	log := logf.FromContext(ctx).WithName("StorageAutoscaling").WithValues("pvc", pvc.Name)
	config := cr.Spec.StorageAutoscaling()

	if usage.UsagePercent < config.TriggerThresholdPercent {
		return false
	}

	if !config.MaxSize.IsZero() {
		currentSize := pvc.Status.Capacity.Storage()
		if currentSize.Cmp(config.MaxSize) >= 0 {
			log.Info("PVC already at maxSize",
				"currentSize", currentSize.String(),
				"maxSize", config.MaxSize.String())
			return false
		}
	}

	for _, cond := range pvc.Status.Conditions {
		if (cond.Type == corev1.PersistentVolumeClaimResizing ||
			cond.Type == corev1.PersistentVolumeClaimFileSystemResizePending) &&
			cond.Status == corev1.ConditionTrue {
			log.V(1).Info("resize already in progress", "condition", cond.Type)
			return false
		}
	}

	return true
}

// calculateNewSize calculates the new PVC size based on current size and growth step
func (r *ReconcilePerconaServerMongoDB) calculateNewSize(
	cr *api.PerconaServerMongoDB,
	pvc *corev1.PersistentVolumeClaim,
) resource.Quantity {
	config := cr.Spec.StorageAutoscaling()
	currentSize := pvc.Status.Capacity.Storage()

	newSizeBytes := currentSize.Value() + config.GrowthStep.Value()
	newSize := *resource.NewQuantity(newSizeBytes, resource.BinarySI)

	if !config.MaxSize.IsZero() && newSize.Cmp(config.MaxSize) > 0 {
		newSize = config.MaxSize
	}

	return newSize
}

// triggerResize records the new requested size, in the CR and in the working
// copy the rest of the reconcile is holding.
func (r *ReconcilePerconaServerMongoDB) triggerResize(
	ctx context.Context,
	pvc *corev1.PersistentVolumeClaim,
	newSize resource.Quantity,
	pvcSpec *api.PVCSpec,
	write storageWriter,
) error {
	if err := write(ctx, newSize); err != nil {
		return err
	}

	// Everything after this pass -- reconcilePVCs, resizeVolumesIfNeeded -- acts
	// on the spec it was handed, and a member group's spec is a copy that no CR
	// write reaches. Mirroring only once the write has landed means there is no
	// size to roll back when the API server rejects one.
	setStorageRequest(pvcSpec, newSize)

	logf.FromContext(ctx).WithName("StorageAutoscaling").WithValues("pvc", pvc.Name).
		Info("storage autoscaling initiated",
			"oldSize", pvc.Status.Capacity.Storage().String(),
			"newSize", newSize.String())

	return nil
}

// updateAutoscalingStatus updates the status for a specific PVC
func (r *ReconcilePerconaServerMongoDB) updateAutoscalingStatus(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	pvcName string,
	usage *PVCUsage,
	err error,
) {
	log := logf.FromContext(ctx).WithName("StorageAutoscaling")

	if pvcName == "" {
		log.V(1).Info("no pvc name specified")
		return
	}

	if cr.Status.StorageAutoscaling == nil {
		cr.Status.StorageAutoscaling = make(map[string]api.StorageAutoscalingStatus)
	}

	status := cr.Status.StorageAutoscaling[pvcName]

	if usage != nil {
		newSize := resource.NewQuantity(usage.TotalBytes, resource.BinarySI)
		if status.CurrentSize != "" {
			oldSize, parseErr := resource.ParseQuantity(status.CurrentSize)
			if parseErr == nil && newSize.Cmp(oldSize) > 0 {
				status.LastResizeTime = metav1.Time{Time: time.Now()}
				status.ResizeCount++
			}
		}
		status.CurrentSize = newSize.String()
		status.LastError = ""
	}

	if err != nil {
		status.LastError = err.Error()
	}

	cr.Status.StorageAutoscaling[pvcName] = status
}

// findPodByName finds a pod in a list by name
func findPodByName(podList *corev1.PodList, podName string) *corev1.Pod {
	for i := range podList.Items {
		if podList.Items[i].Name == podName {
			return &podList.Items[i]
		}
	}
	return nil
}
