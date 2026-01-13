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
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

// reconcileStorageAutoscaling checks PVC disk usage and triggers resize if needed
func (r *ReconcilePerconaServerMongoDB) reconcileStorageAutoscaling(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	sts *appsv1.StatefulSet,
	volumeSpec *api.VolumeSpec,
	ls map[string]string,
) error {
	log := logf.FromContext(ctx).WithName("StorageAutoscaling").WithValues("statefulset", sts.Name)

	if cr.Spec.StorageAutoscaling == nil || !cr.Spec.StorageAutoscaling.Enabled {
		return nil
	}

	if cr.Spec.EnableExternalVolumeAutoscaling {
		log.V(1).Info("skipping storage autoscaling: external autoscaling is enabled")
		return nil
	}

	if volumeSpec == nil || volumeSpec.PersistentVolumeClaim.PersistentVolumeClaimSpec == nil {
		log.V(1).Info("skipping storage autoscaling: not using PVC")
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
		if !validatePVCName(pvc, sts) {
			continue
		}

		podName := extractPodNameFromPVC(pvc.Name, sts.Name)
		pod := findPodByName(podList, podName)
		if pod == nil {
			log.V(1).Info("pod not found for PVC", "pvc", pvc.Name, "pod", podName)
			continue
		}

		if err := r.checkAndResizePVC(ctx, cr, &pvc, pod, volumeSpec); err != nil {
			log.Error(err, "failed to check/resize PVC", "pvc", pvc.Name)
			r.updateAutoscalingStatus(cr, pvc.Name, nil, err)
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
	volumeSpec *api.VolumeSpec,
) error {
	log := logf.FromContext(ctx).WithName("StorageAutoscaling").WithValues("pvc", pvc.Name)

	if pod.Status.Phase != corev1.PodRunning {
		log.V(1).Info("skipping PVC check: pod not running", "phase", pod.Status.Phase)
		return nil
	}

	usage, err := r.getPVCUsageFromMetrics(ctx, pod, pvc.Name)
	if err != nil {
		return errors.Wrap(err, "get PVC usage from metrics")
	}

	r.updateAutoscalingStatus(cr, pvc.Name, usage, nil)

	if !r.shouldTriggerResize(ctx, cr, pvc, usage) {
		return nil
	}

	newSize := r.calculateNewSize(cr, pvc)

	log.Info("triggering storage autoscaling",
		"currentSize", pvc.Status.Capacity.Storage().String(),
		"newSize", newSize.String(),
		"usagePercent", usage.UsagePercent,
		"threshold", cr.Spec.StorageAutoscaling.TriggerThresholdPercent)

	return r.triggerResize(ctx, cr, pvc, newSize, volumeSpec)
}

// shouldTriggerResize determines if a PVC should be resized
func (r *ReconcilePerconaServerMongoDB) shouldTriggerResize(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	pvc *corev1.PersistentVolumeClaim,
	usage *PVCUsage,
) bool {
	log := logf.FromContext(ctx).WithName("StorageAutoscaling").WithValues("pvc", pvc.Name)
	config := cr.Spec.StorageAutoscaling

	if usage.UsagePercent < config.TriggerThresholdPercent {
		log.V(1).Info("usage below threshold",
			"usage", usage.UsagePercent,
			"threshold", config.TriggerThresholdPercent)
		return false
	}

	if config.MaxSize != "" {
		maxSize, err := resource.ParseQuantity(config.MaxSize)
		if err == nil {
			currentSize := pvc.Status.Capacity.Storage()
			if currentSize.Cmp(maxSize) >= 0 {
				log.Info("PVC already at maxSize",
					"currentSize", currentSize.String(),
					"maxSize", maxSize.String())
				return false
			}
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
	config := cr.Spec.StorageAutoscaling
	currentSize := pvc.Status.Capacity.Storage()

	incrementBytes := config.GrowthStepGi * 1024 * 1024 * 1024 // GiB to bytes
	newSizeBytes := currentSize.Value() + incrementBytes
	newSize := *resource.NewQuantity(newSizeBytes, resource.BinarySI)

	gib, err := RoundUpGiB(newSize.Value())
	if err == nil {
		newSize = *resource.NewQuantity(gib*GiB, resource.BinarySI)
	}

	if config.MaxSize != "" {
		maxSize, err := resource.ParseQuantity(config.MaxSize)
		if err == nil && newSize.Cmp(maxSize) > 0 {
			newSize = maxSize
		}
	}

	return newSize
}

// triggerResize updates the CR volumeSpec to trigger a resize operation
func (r *ReconcilePerconaServerMongoDB) triggerResize(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	pvc *corev1.PersistentVolumeClaim,
	newSize resource.Quantity,
	volumeSpec *api.VolumeSpec,
) error {
	log := logf.FromContext(ctx).WithName("StorageAutoscaling").WithValues("pvc", pvc.Name)

	patch := client.MergeFrom(cr.DeepCopy())

	// We are modifying cr directly through the pointer. So the original cr object does get the storage size updated.
	volumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage] = newSize

	if err := r.client.Patch(ctx, cr.DeepCopy(), patch); err != nil {
		return errors.Wrap(err, "patch CR with new storage size")
	}

	if cr.Status.StorageAutoscaling == nil {
		cr.Status.StorageAutoscaling = make(map[string]api.StorageAutoscalingStatus)
	}

	status := cr.Status.StorageAutoscaling[pvc.Name]
	status.LastResizeTime = metav1.Time{Time: time.Now()}
	status.ResizeCount++
	cr.Status.StorageAutoscaling[pvc.Name] = status

	log.Info("storage autoscaling initiated",
		"oldSize", pvc.Status.Capacity.Storage().String(),
		"newSize", newSize.String())

	return nil
}

// updateAutoscalingStatus updates the status for a specific PVC
func (r *ReconcilePerconaServerMongoDB) updateAutoscalingStatus(
	cr *api.PerconaServerMongoDB,
	pvcName string,
	usage *PVCUsage,
	err error,
) {
	if cr.Status.StorageAutoscaling == nil {
		cr.Status.StorageAutoscaling = make(map[string]api.StorageAutoscalingStatus)
	}

	status := cr.Status.StorageAutoscaling[pvcName]

	if usage != nil {
		status.CurrentSize = resource.NewQuantity(usage.TotalBytes, resource.BinarySI).String()
		status.LastError = ""
	}

	if err != nil {
		status.LastError = err.Error()
	}

	cr.Status.StorageAutoscaling[pvcName] = status
}

// extractPodNameFromPVC extracts the pod name from a PVC name
// PVC format: "mongod-data-<statefulset-name>-<index>"
// Pod format: "<statefulset-name>-<index>"
func extractPodNameFromPVC(pvcName string, stsName string) string {
	prefix := config.MongodDataVolClaimName + "-"
	if strings.HasPrefix(pvcName, prefix) {
		return strings.TrimPrefix(pvcName, prefix)
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
