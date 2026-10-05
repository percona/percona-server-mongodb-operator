package perconaservermongodb

import (
	"context"
	"strings"
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
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

// autoscaledVolume describes the volume of a single component that storage
// autoscaling probes and grows. Components don't agree on any of it: replsets
// hold data in mongod-data under /data/db, mongos holds its logs in mongos-logs
// under /data/db/logs, and hidden and non-voting pods name their mongod
// container after their component.
type autoscaledVolume struct {
	// claimName is the volume claim template name, which prefixes every PVC name
	claimName string
	// mountPath is the path to probe with df inside the container
	mountPath string
	// container is the container that mounts the volume
	container string
	// pvcSpec is the part of the CR spec holding the requested size, which is
	// what a resize is triggered through
	pvcSpec *api.PVCSpec
}

// mongodVolume returns the data volume autoscaled for a replset component.
func mongodVolume(volumeSpec *api.VolumeSpec, component string) autoscaledVolume {
	vol := autoscaledVolume{
		claimName: config.MongodDataVolClaimName,
		mountPath: config.MongodContainerDataDir,
		container: naming.MongodContainerName(component),
	}
	if volumeSpec != nil {
		vol.pvcSpec = &volumeSpec.PersistentVolumeClaim
	}
	return vol
}

// mongosVolume returns the log volume autoscaled for mongos.
func mongosVolume(pvcSpec *api.PVCSpec) autoscaledVolume {
	return autoscaledVolume{
		claimName: config.MongosLogVolClaimName,
		mountPath: config.MongodContainerDataLogsDir,
		container: naming.ContainerMongos,
		pvcSpec:   pvcSpec,
	}
}

// reconcileStorageAutoscaling checks PVC disk usage and triggers resize if needed
func (r *ReconcilePerconaServerMongoDB) reconcileStorageAutoscaling(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	sts *appsv1.StatefulSet,
	vol autoscaledVolume,
	ls map[string]string,
) error {
	log := logf.FromContext(ctx).WithName("StorageAutoscaling").WithValues("statefulset", sts.Name)

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

	if vol.pvcSpec == nil || vol.pvcSpec.PersistentVolumeClaimSpec == nil {
		log.V(1).Info("skipping storage autoscaling: not using PVC")
		return nil
	}

	if _, ok := sts.Annotations[api.AnnotationPVCResizeInProgress]; ok {
		log.V(1).Info("PVC resize already in progress")
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
		if !validatePVCName(vol.claimName, pvc, sts) {
			continue
		}

		podName := extractPodNameFromPVC(pvc.Name, vol.claimName)
		pod := findPodByName(podList, podName)
		if pod == nil {
			log.V(1).Info("pod not found for PVC", "pvc", pvc.Name, "pod", podName)
			continue
		}

		if err := r.checkAndResizePVC(ctx, cr, &pvc, pod, vol); err != nil {
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
	vol autoscaledVolume,
) error {
	log := logf.FromContext(ctx).WithName("StorageAutoscaling").WithValues("pvc", pvc.Name)

	if !isContainerAndPodRunning(*pod, vol.container) {
		log.V(1).Info("skipping PVC metrics check: container and pod not running",
			"phase", pod.Status.Phase, "container", vol.container)
		return nil
	}

	usage, err := r.getPVCUsageFromMetrics(ctx, pod, pvc.Name, vol.container, vol.mountPath)
	if err != nil {
		return errors.Wrap(err, "get PVC usage from metrics")
	}

	r.updateAutoscalingStatus(ctx, cr, pvc.Name, usage, nil)

	if !r.shouldTriggerResize(ctx, cr, pvc, usage) {
		return nil
	}

	newSize := r.calculateNewSize(cr, pvc)

	log.Info("triggering storage autoscaling",
		"currentSize", pvc.Status.Capacity.Storage().String(),
		"newSize", newSize.String(),
		"usagePercent", usage.UsagePercent,
		"threshold", cr.Spec.StorageAutoscaling().TriggerThresholdPercent)

	return r.triggerResize(ctx, cr, pvc, newSize, vol.pvcSpec)
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

// triggerResize updates the CR pvcSpec to trigger a resize operation
func (r *ReconcilePerconaServerMongoDB) triggerResize(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	pvc *corev1.PersistentVolumeClaim,
	newSize resource.Quantity,
	pvcSpec *api.PVCSpec,
) error {
	log := logf.FromContext(ctx).WithName("StorageAutoscaling").WithValues("pvc", pvc.Name)

	orig := cr.DeepCopy()

	pvcSpec.Resources.Requests[corev1.ResourceStorage] = newSize

	if err := r.client.Patch(ctx, cr.DeepCopy(), client.MergeFrom(orig)); err != nil {
		return errors.Wrap(err, "patch CR with new storage size")
	}

	log.Info("storage autoscaling initiated",
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

// extractPodNameFromPVC extracts the pod name from a PVC name
// PVC format: "<claim-name>-<statefulset-name>-<index>"
// Pod format: "<statefulset-name>-<index>"
func extractPodNameFromPVC(pvcName string, claimName string) string {
	prefix := claimName + "-"
	if after, ok := strings.CutPrefix(pvcName, prefix); ok {
		return after
	}
	return ""
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
