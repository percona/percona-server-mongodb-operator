package perconaservermongodb

import (
	"context"
	"slices"
	"strings"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

func (r *ReconcilePerconaServerMongoDB) reconcilePVCs(ctx context.Context, cr *api.PerconaServerMongoDB, sts *appsv1.StatefulSet, ls map[string]string, pvcSpec api.PVCSpec) error {
	if err := r.fixVolumeLabels(ctx, sts, ls, pvcSpec); err != nil {
		return errors.Wrap(err, "fix volume labels")
	}

	if err := r.resizeVolumesIfNeeded(ctx, cr, sts, ls, pvcSpec); err != nil {
		return errors.Wrap(err, "resize volumes if needed")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) resizeVolumesIfNeeded(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB, sts *appsv1.StatefulSet, ls map[string]string, pvcSpec psmdbv1.PVCSpec) error {
	log := logf.FromContext(ctx)

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

	if cr.PVCResizeInProgress() {
		resizeInProgress := false
		for _, pvc := range pvcList.Items {
			if !strings.HasPrefix(pvc.Name, psmdb.MongodDataVolClaimName+"-"+sts.Name) {
				continue
			}

			for _, condition := range pvc.Status.Conditions {
				if condition.Status != corev1.ConditionTrue {
					continue
				}

				switch condition.Type {
				case corev1.PersistentVolumeClaimResizing, corev1.PersistentVolumeClaimFileSystemResizePending:
					resizeInProgress = true
					log.V(1).Info(condition.Message, "pvc", pvc.Name, "type", condition.Type, "lastTransitionTime", condition.LastTransitionTime)
					log.Info("PVC resize in progress", "pvc", pvc.Name, "lastTransitionTime", condition.LastTransitionTime)
				}
			}
		}

		if !resizeInProgress {
			if err := k8s.DeannotateObject(ctx, r.client, cr, psmdbv1.AnnotationPVCResizeInProgress); err != nil {
				return errors.Wrap(err, "deannotate pxc")
			}

			log.Info("PVC resize completed")

			return nil
		}
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
		if vct.Name == psmdb.MongodDataVolClaimName {
			volumeTemplate = vct
		}
	}

	requested := pvcSpec.Resources.Requests[corev1.ResourceStorage]
	actual := volumeTemplate.Spec.Resources.Requests[corev1.ResourceStorage]

	if requested.Cmp(actual) < 0 {
		return errors.Wrap(err, "requested storage is less than actual")
	}

	if requested.Cmp(actual) == 0 {
		return nil
	}

	err = k8s.AnnotateObject(ctx, r.client, cr, map[string]string{psmdbv1.AnnotationPVCResizeInProgress: "true"})
	if err != nil {
		return errors.Wrap(err, "annotate pxc")
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
		if !strings.HasPrefix(pvc.Name, psmdb.MongodDataVolClaimName+"-"+sts.Name) {
			continue
		}

		podName := strings.SplitN(pvc.Name, "-", 3)[2]
		if !slices.Contains(podNames, podName) {
			continue
		}

		pvcsToUpdate = append(pvcsToUpdate, pvc.Name)
	}

	log.Info("Resizing PVCs", "requested", requested, "actual", actual, "pvcList", strings.Join(pvcsToUpdate, ","))

	log.Info("Deleting statefulset", "name", sts.Name)

	if err := r.client.Delete(ctx, sts, client.PropagationPolicy("Orphan")); err != nil {
		return errors.Wrapf(err, "delete statefulset/%s", sts.Name)
	}

	for _, pvc := range pvcList.Items {
		if !slices.Contains(pvcsToUpdate, pvc.Name) {
			continue
		}

		log.Info("Resizing PVC", "name", pvc.Name)
		pvc.Spec.Resources.Requests[corev1.ResourceStorage] = requested

		if err := r.client.Update(ctx, &pvc); err != nil {
			return errors.Wrapf(err, "update persistentvolumeclaim/%s", pvc.Name)
		}
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
