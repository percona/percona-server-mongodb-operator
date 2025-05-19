package perconaservermongodb

import (
	"context"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

const (
	GiB = int64(1024 * 1024 * 1024)
)

func (r *ReconcilePerconaServerMongoDB) reconcilePVCs(ctx context.Context, cr *api.PerconaServerMongoDB, sts *appsv1.StatefulSet, ls map[string]string, volumeSpec *api.VolumeSpec) error {
	if err := r.fixVolumeLabels(ctx, sts, ls, volumeSpec.PersistentVolumeClaim); err != nil {
		return errors.Wrap(err, "fix volume labels")
	}

	if err := r.resizeVolumesIfNeeded(ctx, cr, sts, ls, volumeSpec); err != nil {
		return errors.Wrap(err, "resize volumes if needed")
	}

	return nil
}

func validatePVCName(pvc corev1.PersistentVolumeClaim, sts *appsv1.StatefulSet) bool {
	return strings.HasPrefix(pvc.Name, config.MongodDataVolClaimName+"-"+sts.Name)
}

func (r *ReconcilePerconaServerMongoDB) resizeVolumesIfNeeded(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB, sts *appsv1.StatefulSet, ls map[string]string, volumeSpec *api.VolumeSpec) error {
	log := logf.FromContext(ctx).WithName("PVCResize").WithValues("sts", sts.Name)

	pvcSpec := volumeSpec.PersistentVolumeClaim

	pvcList := &corev1.PersistentVolumeClaimList{}
	err := r.client.List(ctx, pvcList, &client.ListOptions{
		Namespace:     sts.Namespace,
		LabelSelector: labels.SelectorFromSet(ls),
	})
	if err != nil {
		return errors.Wrap(err, "list PVCs")
	}

	if len(pvcList.Items) == 0 {
		return nil
	}

	podList := corev1.PodList{}
	if err := r.client.List(ctx, &podList, client.InNamespace(cr.Namespace), client.MatchingLabels(ls)); err != nil {
		return errors.Wrap(err, "list pods")
	}

	podNames := make([]string, 0, len(podList.Items))
	for _, pod := range podList.Items {
		podNames = append(podNames, pod.Name)
	}

	pvcsToUpdate := make([]string, 0, len(pvcList.Items))
	for _, pvc := range pvcList.Items {
		if !validatePVCName(pvc, sts) {
			continue
		}

		podName := strings.SplitN(pvc.Name, "-", 3)[2]
		if !slices.Contains(podNames, podName) {
			continue
		}

		pvcsToUpdate = append(pvcsToUpdate, pvc.Name)
	}

	var actual resource.Quantity
	for _, pvc := range pvcList.Items {
		if !validatePVCName(pvc, sts) {
			continue
		}

		if pvc.Status.Capacity == nil || pvc.Status.Capacity.Storage() == nil {
			continue
		}

		// we need to find the smallest size among all PVCs
		// since it indicates a resize operation is failed
		if actual.IsZero() || pvc.Status.Capacity.Storage().Cmp(actual) < 0 {
			actual = *pvc.Status.Capacity.Storage()
		}
	}

	if actual.IsZero() {
		return nil
	}

	sts = sts.DeepCopy()
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(sts), sts); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return errors.Wrapf(err, "get statefulset %s", client.ObjectKeyFromObject(sts))
	}

	var volumeTemplate corev1.PersistentVolumeClaim
	for _, vct := range sts.Spec.VolumeClaimTemplates {
		if vct.Name == config.MongodDataVolClaimName {
			volumeTemplate = vct
		}
	}

	requested := pvcSpec.Resources.Requests[corev1.ResourceStorage]
	gib, err := RoundUpGiB(requested.Value())
	if err != nil {
		return errors.Wrap(err, "round GiB value")
	}

	requested = *resource.NewQuantity(gib*GiB, resource.BinarySI)
	configured := volumeTemplate.Spec.Resources.Requests[corev1.ResourceStorage]

	if sts.Annotations[psmdbv1.AnnotationPVCResizeInProgress] != "" {
		resizeStartedAt, err := time.Parse(time.RFC3339, sts.Annotations[psmdbv1.AnnotationPVCResizeInProgress])
		if err != nil {
			return errors.Wrap(err, "parse annotation")
		}

		updatedPVCs := 0
		for _, pvc := range pvcList.Items {
			if !validatePVCName(pvc, sts) {
				continue
			}

			if pvc.Status.Capacity.Storage().Cmp(requested) == 0 {
				updatedPVCs++
				log.Info("PVC resize finished", "name", pvc.Name, "size", pvc.Status.Capacity.Storage())
				continue
			}

			for _, condition := range pvc.Status.Conditions {
				if condition.Status != corev1.ConditionTrue {
					continue
				}

				switch condition.Type {
				case corev1.PersistentVolumeClaimResizing, corev1.PersistentVolumeClaimFileSystemResizePending:
					log.V(1).Info(condition.Message, "pvc", pvc.Name, "type", condition.Type, "lastTransitionTime", condition.LastTransitionTime)
					log.Info("PVC resize in progress", "pvc", pvc.Name, "lastTransitionTime", condition.LastTransitionTime)
				}
			}

			events := &eventsv1.EventList{}
			if err := r.client.List(ctx, events, &client.ListOptions{
				Namespace:     sts.Namespace,
				FieldSelector: fields.SelectorFromSet(map[string]string{"regarding.name": pvc.Name}),
			}); err != nil {
				return errors.Wrapf(err, "list events for pvc/%s", pvc.Name)
			}

			for _, event := range events.Items {
				eventTime := event.EventTime.Time
				if event.EventTime.IsZero() {
					eventTime = event.DeprecatedFirstTimestamp.Time
				}

				if eventTime.Before(resizeStartedAt) {
					continue
				}

				switch event.Reason {
				case "Resizing", "ExternalExpanding", "FileSystemResizeRequired":
					log.Info("PVC resize in progress", "pvc", pvc.Name, "reason", event.Reason, "message", event.Note)
				case "FileSystemResizeSuccessful":
					log.Info("PVC resize completed", "pvc", pvc.Name, "reason", event.Reason, "message", event.Note)
				case "VolumeResizeFailed":
					log.Error(nil, "PVC resize failed", "pvc", pvc.Name, "reason", event.Reason, "message", event.Note)

					if err := r.handlePVCResizeFailure(ctx, cr, sts, configured); err != nil {
						return err
					}

					return errors.Errorf("volume resize failed: %s", event.Note)
				}
			}
		}

		if updatedPVCs == len(pvcsToUpdate) {
			log.Info("Deleting statefulset")

			if err := r.client.Delete(ctx, sts, client.PropagationPolicy("Orphan")); err != nil {
				if k8serrors.IsNotFound(err) {
					return nil
				}
				return errors.Wrapf(err, "delete statefulset/%s", sts.Name)
			}

			log.Info("PVC resize completed")

			return nil
		}
	}

	if requested.Cmp(actual) < 0 {
		return errors.Errorf("requested storage (%s) is less than actual storage (%s)", requested.String(), actual.String())
	}

	if requested.Cmp(actual) == 0 {
		return nil
	}

	if cr.CompareVersion("1.18.0") >= 0 && !cr.Spec.VolumeExpansionEnabled {
		// If expansion is disabled we should keep the old value
		pvcSpec.Resources.Requests[corev1.ResourceStorage] = configured
		return nil
	}

	err = k8s.AnnotateObject(ctx, r.client, sts, map[string]string{psmdbv1.AnnotationPVCResizeInProgress: metav1.Now().Format(time.RFC3339)})
	if err != nil {
		return errors.Wrap(err, "annotate psmdb")
	}

	log.Info("Resizing PVCs", "requested", requested, "actual", actual, "pvcList", strings.Join(pvcsToUpdate, ","))

	for _, pvc := range pvcList.Items {
		if !slices.Contains(pvcsToUpdate, pvc.Name) {
			continue
		}

		if pvc.Status.Capacity.Storage().Cmp(requested) == 0 {
			log.Info("PVC already resized", "name", pvc.Name, "actual", pvc.Status.Capacity.Storage(), "requested", requested)
			continue
		}

		log.Info("Resizing PVC", "name", pvc.Name, "actual", pvc.Status.Capacity.Storage(), "requested", requested)
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = requested

		if err := r.client.Update(ctx, &pvc); err != nil {
			switch {
			case strings.Contains(err.Error(), "exceeded quota"):
				log.Error(err, "PVC resize failed", "reason", "ExceededQuota", "message", err.Error())

				if err := r.handlePVCResizeFailure(ctx, cr, sts, configured); err != nil {
					return err
				}

				return nil
			case strings.Contains(err.Error(), "the storageclass that provisions the pvc must support resize"):
				log.Error(err, "PVC resize failed", "reason", "StorageClassNotSupportResize", "message", err.Error())

				if err := r.handlePVCResizeFailure(ctx, cr, sts, configured); err != nil {
					return err
				}

				return nil
			default:
				return errors.Wrapf(err, "update persistentvolumeclaim/%s", pvc.Name)
			}
		}

		log.Info("PVC resize started", "pvc", pvc.Name, "requested", requested)
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) handlePVCResizeFailure(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB, sts *appsv1.StatefulSet, originalSize resource.Quantity) error {
	if err := r.revertVolumeTemplate(ctx, cr, sts, originalSize); err != nil {
		return errors.Wrapf(err, "revert volume template for sts/%s", sts.Name)
	}

	if err := k8s.DeannotateObject(ctx, r.client, sts, psmdbv1.AnnotationPVCResizeInProgress); err != nil {
		return errors.Wrapf(err, "deannotate psmdb/%s", cr.Name)
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) revertVolumeTemplate(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB, sts *appsv1.StatefulSet, originalSize resource.Quantity) error {
	log := logf.FromContext(ctx)

	orig := cr.DeepCopy()

	replset, ok := sts.Labels[naming.LabelKubernetesReplset]
	if !ok {
		return errors.New("missing component label")
	}

	if replset == psmdbv1.ConfigReplSetName {
		log.Info("Reverting volume template for configsvr", "originalSize", originalSize)
		cr.Spec.Sharding.ConfigsvrReplSet.VolumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage] = originalSize
	} else {
		for _, rs := range cr.Spec.Replsets {
			if rs.Name == replset {
				log.Info("Reverting volume template for replset", "replset", replset, "originalSize", originalSize)
				rs.VolumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage] = originalSize
			}
		}
	}

	if err := r.client.Patch(ctx, cr.DeepCopy(), client.MergeFrom(orig)); err != nil {
		return errors.Wrapf(err, "patch psmdb/%s", cr.Name)
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) fixVolumeLabels(ctx context.Context, sts *appsv1.StatefulSet, ls map[string]string, pvcSpec psmdbv1.PVCSpec) error {
	pvcList := &corev1.PersistentVolumeClaimList{}
	err := r.client.List(ctx, pvcList, &client.ListOptions{
		Namespace:     sts.Namespace,
		LabelSelector: labels.SelectorFromSet(ls),
	})
	if err != nil {
		return errors.Wrap(err, "list PVCs")
	}

	for _, pvc := range pvcList.Items {
		orig := pvc.DeepCopy()

		if pvc.Labels == nil {
			pvc.Labels = make(map[string]string)
		}
		for k, v := range pvcSpec.Labels {
			pvc.Labels[k] = v
		}
		for k, v := range sts.Labels {
			pvc.Labels[k] = v
		}

		if pvc.Annotations == nil {
			pvc.Annotations = make(map[string]string)
		}
		for k, v := range pvcSpec.Annotations {
			pvc.Annotations[k] = v
		}

		if util.MapEqual(orig.Labels, pvc.Labels) && util.MapEqual(orig.Annotations, pvc.Annotations) {
			continue
		}
		patch := client.MergeFrom(orig)

		if err := r.client.Patch(ctx, &pvc, patch); err != nil {
			logf.FromContext(ctx).Error(err, "patch PVC", "PVC", pvc.Name)
		}
	}

	return nil
}

func roundUpSize(volumeSizeBytes int64, allocationUnitBytes int64) int64 {
	if allocationUnitBytes == 0 {
		return 0 // Avoid division by zero
	}
	return (volumeSizeBytes + allocationUnitBytes - 1) / allocationUnitBytes
}

// RoundUpGiB rounds up the volume size in bytes upto multiplications of GiB
// in the unit of GiB
func RoundUpGiB(volumeSizeBytes int64) (int64, error) {
	result := roundUpSize(volumeSizeBytes, GiB)
	if result > int64(math.MaxInt64) {
		return 0, errors.Errorf("rounded up size exceeds maximum value of int64: %d", result)
	}
	return result, nil
}
