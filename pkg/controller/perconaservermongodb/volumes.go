package perconaservermongodb

import (
	"context"
	"fmt"
	"maps"
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

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

func (r *ReconcilePerconaServerMongoDB) reconcilePVCs(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB, sts *appsv1.StatefulSet, ls map[string]string, volumeSpec *psmdbv1.VolumeSpec) error {
	if err := r.fixVolumeLabels(ctx, sts, ls, volumeSpec.PersistentVolumeClaim); err != nil {
		return errors.Wrap(err, "fix volume labels")
	}

	if err := r.resizeVolumesIfNeeded(ctx, cr, sts, ls, config.MongodDataVolClaimName, volumeSpec.PersistentVolumeClaim); err != nil {
		return errors.Wrap(err, "resize volumes if needed")
	}

	return nil
}

func validatePVCName(claimName string, pvc corev1.PersistentVolumeClaim, sts *appsv1.StatefulSet) bool {
	return strings.HasPrefix(pvc.Name, claimName+"-"+sts.Name)
}

func maxPVCCapacity(pvcs []corev1.PersistentVolumeClaim, claimName string, sts *appsv1.StatefulSet) resource.Quantity {
	var max resource.Quantity
	for _, pvc := range pvcs {
		if !validatePVCName(claimName, pvc, sts) {
			continue
		}
		if pvc.Status.Capacity != nil && pvc.Status.Capacity.Storage() != nil {
			if max.IsZero() || pvc.Status.Capacity.Storage().Cmp(max) > 0 {
				max = *pvc.Status.Capacity.Storage()
			}
		}
	}
	return max
}

func isPVCExpansionInProgress(pvcs []corev1.PersistentVolumeClaim, claimName string, sts *appsv1.StatefulSet) (string, bool) {
	for _, pvc := range pvcs {
		if !validatePVCName(claimName, pvc, sts) {
			continue
		}
		for _, cond := range pvc.Status.Conditions {
			if cond.Status != corev1.ConditionTrue {
				continue
			}
			if cond.Type == corev1.PersistentVolumeClaimResizing ||
				cond.Type == corev1.PersistentVolumeClaimFileSystemResizePending {
				return pvc.Name, true
			}
		}
		if pvc.Spec.Resources.Requests != nil && pvc.Status.Capacity != nil {
			specSize := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
			statusSize := pvc.Status.Capacity[corev1.ResourceStorage]
			if specSize.Cmp(statusSize) > 0 {
				return pvc.Name, true
			}
		}
	}
	return "", false
}

func (r *ReconcilePerconaServerMongoDB) resizeVolumesIfNeeded(
	ctx context.Context,
	cr *psmdbv1.PerconaServerMongoDB,
	sts *appsv1.StatefulSet,
	ls map[string]string,
	claimName string,
	pvcSpec psmdbv1.PVCSpec,
) error {
	if cr.Spec.IsExternalVolumeAutoscalingEnabled() {
		return nil
	}

	// Check if a shrink operation is already in progress for this STS.
	if state, ok := cr.GetPVCShrinkState(sts.Name); ok {
		return r.shrinkVolumesIfNeeded(ctx, cr, sts, ls, claimName, pvcSpec, state)
	}

	log := logf.FromContext(ctx).WithName("PVCResize").WithValues("sts", sts.Name)

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
		if !validatePVCName(claimName, pvc, sts) {
			continue
		}

		podName := pvc.Name[len(claimName)+1:]
		if !slices.Contains(podNames, podName) {
			continue
		}

		pvcsToUpdate = append(pvcsToUpdate, pvc.Name)
	}

	var actual resource.Quantity
	for _, pvc := range pvcList.Items {
		if !validatePVCName(claimName, pvc, sts) {
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

	origSts := sts
	sts = sts.DeepCopy()
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(sts), sts); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return errors.Wrapf(err, "get statefulset %s", client.ObjectKeyFromObject(sts))
	}

	var volumeTemplate corev1.PersistentVolumeClaim
	for _, vct := range sts.Spec.VolumeClaimTemplates {
		if vct.Name == claimName {
			volumeTemplate = vct
		}
	}

	requested := pvcSpec.Resources.Requests[corev1.ResourceStorage]
	configured := volumeTemplate.Spec.Resources.Requests[corev1.ResourceStorage]

	if sts.Annotations[psmdbv1.AnnotationPVCResizeInProgress] != "" {
		resizeStartedAt, err := time.Parse(time.RFC3339, sts.Annotations[psmdbv1.AnnotationPVCResizeInProgress])
		if err != nil {
			return errors.Wrap(err, "parse annotation")
		}

		updatedPVCs := 0
		for _, pvc := range pvcList.Items {
			if !slices.Contains(pvcsToUpdate, pvc.Name) {
				continue
			}

			if pvc.Status.Capacity.Storage().Cmp(requested) >= 0 {
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
					// the external resizer retries transient CSI errors, so the resize can still succeed
					if strings.Contains(event.Note, "DeadlineExceeded") || strings.Contains(event.Note, "Unavailable") {
						log.Info("PVC resize failed with a retriable error, waiting for retry", "pvc", pvc.Name, "message", event.Note)
						continue
					}

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

			if err := r.deleteStatefulSetForResize(ctx, cr, sts); err != nil {
				return err
			}

			log.Info("PVC resize completed")

			return nil
		}
	}

	if requested.Cmp(actual) < 0 {
		if cr.Spec.IsVolumeShrinkEnabled() {
			err := r.shrinkVolumesIfNeeded(ctx, cr, sts, ls, claimName, pvcSpec, psmdbv1.PVCShrinkState{})
			// Always align the original STS VolumeClaimTemplate with the actual
			// PVC size so that createOrUpdate (which runs after reconcilePVCs)
			// does not attempt to change the immutable VCT field.
			for i := range origSts.Spec.VolumeClaimTemplates {
				if origSts.Spec.VolumeClaimTemplates[i].Name == claimName {
					origSts.Spec.VolumeClaimTemplates[i].Spec.Resources.Requests[corev1.ResourceStorage] = actual
					break
				}
			}
			return err
		}
		if configured.Cmp(requested) == 0 {
			return nil
		}
		if configured.Cmp(requested) < 0 {
			log.Info("Deleting statefulset with stale volume claim template")

			if err := r.deleteStatefulSetForResize(ctx, cr, sts); err != nil {
				return err
			}
			return nil
		}
		if err := r.revertVolumeTemplate(ctx, cr, sts, configured); err != nil {
			return errors.Wrapf(err, "revert volume template for sts/%s", sts.Name)
		}
		if cr.Spec.IsVolumeExpansionEnabled() {
			return errors.Errorf("requested storage (%s) is less than actual storage (%s)", requested.String(), actual.String())
		}
	}
	if requested.Cmp(actual) == 0 {
		if configured.Cmp(requested) != 0 {
			log.Info("Deleting statefulset with stale volume claim template")

			if err := r.deleteStatefulSetForResize(ctx, cr, sts); err != nil {
				return err
			}
		}
		return nil
	}

	if cr.CompareVersion("1.18.0") >= 0 && !cr.Spec.IsVolumeExpansionEnabled() {
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

		// Re-read the PVC to get the latest resourceVersion before updating,
		// as it may have been modified since the initial list (e.g. by fixVolumeLabels
		// or by the Kubernetes PVC controller).
		if err := r.client.Get(ctx, client.ObjectKeyFromObject(&pvc), &pvc); err != nil {
			return errors.Wrapf(err, "get persistentvolumeclaim/%s", pvc.Name)
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

func (r *ReconcilePerconaServerMongoDB) deleteStatefulSetForResize(
	ctx context.Context,
	cr *psmdbv1.PerconaServerMongoDB,
	sts *appsv1.StatefulSet,
) error {
	log := logf.FromContext(ctx).WithName("PVCResize").WithValues("sts", sts.Name)

	if restartedAtValue, exists := sts.Spec.Template.Annotations[naming.AnnotationKubectlRestartedAt]; exists {
		if err := k8s.AnnotateObject(ctx, r.client, cr, map[string]string{psmdbv1.AnnotationPreservedRestartedAt(sts.Name): restartedAtValue}); err != nil {
			log.V(1).Info("Failed to preserve restartedAt annotation, continuing", "error", err)
		}
	}

	if err := r.client.Delete(ctx, sts, client.PropagationPolicy(metav1.DeletePropagationOrphan)); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return errors.Wrapf(err, "delete statefulset/%s", sts.Name)
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

	component, ok := sts.Labels[naming.LabelKubernetesComponent]
	if !ok {
		return errors.New("missing component label")
	}

	switch component {
	case naming.ComponentMongod, naming.ComponentNonVoting, naming.ComponentHidden:
		replset, ok := sts.Labels[naming.LabelKubernetesReplset]
		if !ok {
			return errors.New("missing replset label")
		}

		rs := cr.Spec.Replset(replset)
		if rs == nil {
			return errors.Errorf("replset %s not found in cr", replset)
		}

		volumeSpec := rs.VolumeSpec
		switch component {
		case naming.ComponentNonVoting:
			volumeSpec = rs.NonVoting.VolumeSpec
		case naming.ComponentHidden:
			volumeSpec = rs.Hidden.VolumeSpec
		}

		if volumeSpec == nil || volumeSpec.PersistentVolumeClaim.Resources.Requests == nil {
			return errors.Errorf("missing volume spec for %s/%s", replset, component)
		}

		log.Info("Reverting volume template for replset", "replset", replset, "component", component, "originalSize", originalSize)
		volumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage] = originalSize
	case naming.ComponentMongos:
		pvcSpec := cr.Spec.Sharding.Mongos.LogStorage()
		if pvcSpec == nil || pvcSpec.Resources.Requests == nil {
			return errors.New("missing log storage spec for mongos")
		}

		log.Info("Reverting volume template for mongos", "originalSize", originalSize)
		pvcSpec.Resources.Requests[corev1.ResourceStorage] = originalSize
	case naming.ComponentConfigSrv:
		log.Info("Reverting volume template for configsvr", "originalSize", originalSize)
		cr.Spec.Sharding.ConfigsvrReplSet.VolumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage] = originalSize
	default:
		return errors.Errorf("unsupported component %s", component)
	}

	if err := r.client.Patch(ctx, cr.DeepCopy(), client.MergeFrom(orig)); err != nil {
		return errors.Wrapf(err, "patch psmdb/%s", cr.Name)
	}

	return nil
}

type shrinkContext struct {
	cr            *psmdbv1.PerconaServerMongoDB
	sts           *appsv1.StatefulSet
	ls            map[string]string
	claimName     string
	pvcSpec       psmdbv1.PVCSpec
	component     string
	replset       *psmdbv1.ReplsetSpec
	isRSComponent bool
	needsStepdown bool
}

func (r *ReconcilePerconaServerMongoDB) shrinkVolumesIfNeeded(
	ctx context.Context,
	cr *psmdbv1.PerconaServerMongoDB,
	sts *appsv1.StatefulSet,
	ls map[string]string,
	claimName string,
	pvcSpec psmdbv1.PVCSpec,
	state psmdbv1.PVCShrinkState,
) error {
	component, ok := sts.Labels[naming.LabelKubernetesComponent]
	if !ok {
		return errors.New("missing component label")
	}

	replsetName, _ := sts.Labels[naming.LabelKubernetesReplset]
	var replset *psmdbv1.ReplsetSpec
	if replsetName != "" {
		replset = cr.Spec.Replset(replsetName)
	}

	sc := shrinkContext{
		cr:            cr,
		sts:           sts,
		ls:            ls,
		claimName:     claimName,
		pvcSpec:       pvcSpec,
		component:     component,
		replset:       replset,
		isRSComponent: component != naming.ComponentMongos,
		needsStepdown: component == naming.ComponentMongod || component == naming.ComponentConfigSrv,
	}

	// Route to the correct phase
	switch state.Phase {
	case "":
		pvcList := corev1.PersistentVolumeClaimList{}
		if err := r.client.List(ctx, &pvcList, client.InNamespace(cr.Namespace), client.MatchingLabels(ls)); err != nil {
			return errors.Wrap(err, "list pvcs for shrink")
		}
		if pvcName, expanding := isPVCExpansionInProgress(pvcList.Items, claimName, sts); expanding {
			logf.FromContext(ctx).WithName("PVCShrink").Info("PVC expansion still in progress, deferring shrink", "sts", sts.Name, "pvc", pvcName)
			return nil
		}
		actual := maxPVCCapacity(pvcList.Items, claimName, sts)
		requested := pvcSpec.Resources.Requests[corev1.ResourceStorage]
		return r.shrinkPreflight(ctx, sc, requested, actual)
	case "secondaries":
		return r.shrinkProcessPods(ctx, sc, state, false)
	case "stepdown":
		return r.shrinkStepdown(ctx, sc, state)
	case "primary":
		return r.shrinkProcessPods(ctx, sc, state, true)
	default:
		return errors.Errorf("unknown shrink phase: %s", state.Phase)
	}
}

func (r *ReconcilePerconaServerMongoDB) shrinkPreflight(
	ctx context.Context,
	sc shrinkContext,
	requested resource.Quantity,
	actual resource.Quantity,
) error {
	log := logf.FromContext(ctx).WithName("PVCShrink").WithValues("sts", sc.sts.Name)

	// revertVCT sets the VolumeClaimTemplate size back to the actual PVC size
	// in the in-memory STS object. This prevents createOrUpdate from attempting
	// to change the immutable VolumeClaimTemplate when the shrink is deferred.
	revertVCT := func() {
		for i := range sc.sts.Spec.VolumeClaimTemplates {
			if sc.sts.Spec.VolumeClaimTemplates[i].Name == sc.claimName {
				sc.sts.Spec.VolumeClaimTemplates[i].Spec.Resources.Requests[corev1.ResourceStorage] = actual
				break
			}
		}
	}

	clusterReady := sc.cr.Status.State == psmdbv1.AppStateReady ||
		(sc.cr.Status.State == psmdbv1.AppStateInit && sc.cr.IsPVCShrinkInProgress())
	if !clusterReady {
		log.Info("PVC shrink waiting for cluster to be ready", "state", string(sc.cr.Status.State))
		revertVCT()
		return nil
	}

	isBackup, err := r.isBackupRunning(ctx, sc.cr)
	if err != nil {
		return errors.Wrap(err, "check active backups")
	}
	if isBackup {
		log.Info("PVC shrink waiting for backup to complete")
		revertVCT()
		return nil
	}

	isRestore, err := r.isRestoreRunning(ctx, sc.cr)
	if err != nil {
		return errors.Wrap(err, "check active restores")
	}
	if isRestore {
		log.Info("PVC shrink waiting for restore to complete")
		revertVCT()
		return nil
	}

	if r.newPBM != nil {
		hasActiveJobs, err := backup.HasActiveJobs(ctx, r.newPBM, r.client, sc.cr, backup.Job{}, backup.NotPITRLock)
		if err != nil {
			return errors.Wrap(err, "check active backup jobs")
		}
		if hasActiveJobs {
			log.Info("PVC shrink waiting for active backup jobs to complete")
			revertVCT()
			return nil
		}
	}

	// Reject single-member replica set (no sync source for initial sync)
	if sc.isRSComponent && sc.replset != nil && sc.replset.Size == 1 {
		if err := r.revertVolumeTemplate(ctx, sc.cr, sc.sts, actual); err != nil {
			return errors.Wrapf(err, "revert volume template for sts/%s", sc.sts.Name)
		}
		return errors.Errorf("PVC shrink rejected for %s: single-member replica set has no sync source", sc.sts.Name)
	}

	// Disk usage validation: verify each pod has 20% headroom after shrink
	podList := corev1.PodList{}
	if err := r.client.List(ctx, &podList, client.InNamespace(sc.cr.Namespace), client.MatchingLabels(sc.ls)); err != nil {
		return errors.Wrap(err, "list pods for disk check")
	}

	containerName := naming.MongodContainerName(sc.component)
	mountPath := ""
	if sc.component == naming.ComponentMongos {
		containerName = naming.ContainerMongos
		mountPath = config.MongodContainerDataLogsDir
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		usage, err := r.getPVCUsageFromMetrics(ctx, pod, sc.claimName+"-"+pod.Name, containerName, mountPath)
		if err != nil {
			log.Info("Failed to get PVC usage, deferring shrink", "pod", pod.Name, "error", err)
			revertVCT()
			return nil
		}

		maxUsageRatio := sc.cr.Spec.ShrinkMaxDiskUsagePercent()
		maxUsed := float64(requested.Value()) * maxUsageRatio
		if float64(usage.UsedBytes) > maxUsed {
			log.Info("PVC shrink rejected: disk usage exceeds threshold of target size",
				"pod", pod.Name, "usedBytes", usage.UsedBytes, "targetSize", requested.String(), "maxAllowed", int64(maxUsed), "thresholdPercent", int(maxUsageRatio*100))
			if err := r.revertVolumeTemplate(ctx, sc.cr, sc.sts, actual); err != nil {
				return errors.Wrapf(err, "revert volume template for sts/%s", sc.sts.Name)
			}
			return errors.Errorf("PVC shrink rejected: pod %s disk usage exceeds %d%% of target size %s", pod.Name, int(maxUsageRatio*100), requested.String())
		}
	}

	// RS health check (skip for mongos)
	if sc.isRSComponent && sc.replset != nil {
		rsClient, err := r.mongoClientWithRole(ctx, sc.cr, sc.replset, psmdbv1.RoleClusterAdmin)
		if err != nil {
			log.Info("PVC shrink waiting: failed to create mongo client for RS health check", "error", err)
			revertVCT()
			return nil
		}
		defer func() {
			if err := rsClient.Disconnect(ctx); err != nil {
				log.Error(err, "failed to disconnect mongo client")
			}
		}()

		rsStatus, err := rsClient.RSStatus(ctx)
		if err != nil {
			log.Info("PVC shrink waiting: failed to get RS status", "error", err)
			revertVCT()
			return nil
		}

		for _, member := range rsStatus.Members {
			if member.StateStr != "PRIMARY" && member.StateStr != "SECONDARY" && member.StateStr != "ARBITER" {
				log.Info("PVC shrink waiting: member not in healthy state", "member", member.Name, "state", member.StateStr)
				revertVCT()
				return nil
			}
		}
	}

	// Disable balancer for sharded clusters when shrinking shard or configsvr
	if sc.cr.Spec.Sharding.Enabled && (sc.component == naming.ComponentMongod || sc.component == naming.ComponentConfigSrv) {
		if err := r.disableBalancer(ctx, sc.cr); err != nil {
			return errors.Wrap(err, "disable balancer for PVC shrink")
		}
	}

	// Identify primary pod
	var primaryPod string
	if sc.isRSComponent && sc.replset != nil {
		for i := range podList.Items {
			isPrimary, err := r.isPodPrimary(ctx, sc.cr, podList.Items[i], sc.replset)
			if err != nil {
				log.Info("Failed to check if pod is primary", "pod", podList.Items[i].Name, "error", err)
				continue
			}
			if isPrimary {
				primaryPod = podList.Items[i].Name
				break
			}
		}
	}

	if sc.isRSComponent && sc.needsStepdown && primaryPod == "" {
		log.Info("PVC shrink waiting: could not identify primary pod")
		revertVCT()
		return nil
	}

	// Delete STS with cascade=orphan (reuse existing function)
	if err := r.deleteStatefulSetForResize(ctx, sc.cr, sc.sts); err != nil {
		return errors.Wrap(err, "delete statefulset for shrink")
	}

	log.Info("PVC shrink preflight passed, starting shrink operation",
		"targetSize", requested.String(), "primaryPod", primaryPod)

	// Store shrink state in CR status
	sc.cr.SetPVCShrinkState(sc.sts.Name, psmdbv1.PVCShrinkState{
		TargetSize:    requested.String(),
		StartedAt:     time.Now().Format(time.RFC3339),
		Phase:         "secondaries",
		ProcessedPods: []string{},
		PrimaryPod:    primaryPod,
	})

	return nil
}

func (r *ReconcilePerconaServerMongoDB) shrinkProcessPods(
	ctx context.Context,
	sc shrinkContext,
	state psmdbv1.PVCShrinkState,
	isPrimaryPhase bool,
) error {
	log := logf.FromContext(ctx).WithName("PVCShrink").WithValues("sts", sc.sts.Name, "phase", state.Phase)

	// Block on active backups/restores
	isBackup, err := r.isBackupRunning(ctx, sc.cr)
	if err != nil {
		return errors.Wrap(err, "check active backups")
	}
	if isBackup {
		log.Info("PVC shrink waiting for backup to complete")
		return nil
	}

	isRestore, err := r.isRestoreRunning(ctx, sc.cr)
	if err != nil {
		return errors.Wrap(err, "check active restores")
	}
	if isRestore {
		log.Info("PVC shrink waiting for restore to complete")
		return nil
	}

	targetSize := resource.MustParse(state.TargetSize)

	// List pods
	podList := corev1.PodList{}
	if err := r.client.List(ctx, &podList, client.InNamespace(sc.cr.Namespace), client.MatchingLabels(sc.ls)); err != nil {
		return errors.Wrap(err, "list pods")
	}

	// Sort descending by ordinal (highest first)
	sortPodsByOrdinal(podList.Items, func(i, j int) bool { return i > j })

	// List PVCs
	pvcList := corev1.PersistentVolumeClaimList{}
	if err := r.client.List(ctx, &pvcList, client.InNamespace(sc.cr.Namespace), client.MatchingLabels(sc.ls)); err != nil {
		return errors.Wrap(err, "list pvcs")
	}

	// Build PVC lookup map
	pvcByPod := make(map[string]*corev1.PersistentVolumeClaim)
	for i := range pvcList.Items {
		pvc := &pvcList.Items[i]
		if !validatePVCName(sc.claimName, *pvc, sc.sts) {
			continue
		}
		podName := pvc.Name[len(sc.claimName)+1:]
		pvcByPod[podName] = pvc
	}

	// Make sure no expected pod is missing (deleted, not yet recreated by the
	// STS controller). Otherwise we could take down a second member.
	var replicas int
	switch {
	case sc.sts.Spec.Replicas != nil:
		replicas = int(*sc.sts.Spec.Replicas)
	case sc.replset != nil && sc.isRSComponent:
		replicas = int(sc.replset.Size)
	default:
		log.Info("Waiting for statefulset replicas to be known")
		return nil
	}
	for i := 0; i < replicas; i++ {
		expectedName := fmt.Sprintf("%s-%d", sc.sts.Name, i)
		if !isPrimaryPhase && expectedName == state.PrimaryPod {
			continue
		}
		if isPrimaryPhase && expectedName != state.PrimaryPod {
			continue
		}
		if slices.Contains(state.ProcessedPods, expectedName) {
			continue
		}
		found := slices.ContainsFunc(podList.Items, func(p corev1.Pod) bool { return p.Name == expectedName })
		if !found {
			log.Info("Waiting for missing pod to be recreated", "pod", expectedName)
			return nil
		}
	}

	for _, pod := range podList.Items {
		// In secondaries phase, skip the primary
		if !isPrimaryPhase && pod.Name == state.PrimaryPod {
			continue
		}
		// In primary phase, only process the primary
		if isPrimaryPhase && pod.Name != state.PrimaryPod {
			continue
		}

		// Skip already processed pods
		if slices.Contains(state.ProcessedPods, pod.Name) {
			continue
		}

		pvc := pvcByPod[pod.Name]

		// If PVC is missing (deleted but pod not yet recreated), wait
		if pvc == nil {
			log.Info("Waiting for PVC to be recreated", "pod", pod.Name)
			return nil
		}

		// If PVC was provisioned with the old (larger) request, delete it and the pod.
		// Use Spec.Requests, not Status.Capacity, because storage classes may round capacity up.
		pvcRequested := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
		if pvcRequested.Cmp(targetSize) > 0 {
			log.Info("Deleting PVC for shrink", "pvc", pvc.Name, "pod", pod.Name)
			if err := r.client.Delete(ctx, pvc); err != nil && !k8serrors.IsNotFound(err) {
				return errors.Wrapf(err, "delete PVC %s", pvc.Name)
			}
			log.Info("Deleting pod for shrink", "pod", pod.Name)
			if err := r.client.Delete(ctx, &pod); err != nil && !k8serrors.IsNotFound(err) {
				return errors.Wrapf(err, "delete pod %s", pod.Name)
			}
			return nil
		}

		// Pod not Ready yet (initial sync in progress), wait
		if !isPodReady(pod) {
			log.Info("Waiting for pod to become ready", "pod", pod.Name)
			return nil
		}

		// PVC has new size and pod is Ready
		if sc.isRSComponent && sc.replset != nil {
			isPrimary, err := r.isPodPrimary(ctx, sc.cr, pod, sc.replset)
			if err != nil {
				log.Info("Failed to check RS membership, waiting", "pod", pod.Name, "error", err)
				return nil
			}
			if isPrimary {
				if !isPrimaryPhase {
					// Primary election happened during shrink. Update the
					// tracked primary so this pod is skipped and processed
					// in the primary phase instead. Return so the next
					// reconcile revisits the former primary as a secondary.
					log.Info("Pod became primary during shrink, updating tracked primary", "pod", pod.Name, "previousPrimary", state.PrimaryPod)
					state.PrimaryPod = pod.Name
					sc.cr.SetPVCShrinkState(sc.sts.Name, state)
					return nil
				}
				log.Info("Waiting for pod to rejoin as SECONDARY", "pod", pod.Name)
				return nil
			}
		}

		// Pod is ready and healthy, mark as processed
		log.Info("Pod shrink complete", "pod", pod.Name)
		state.ProcessedPods = append(state.ProcessedPods, pod.Name)
	}

	// All pods in this phase are done
	if !isPrimaryPhase {
		state.Phase = "stepdown"
	} else {
		sc.cr.ClearPVCShrinkState(sc.sts.Name)
		log.Info("PVC shrink completed successfully")
		return nil
	}

	sc.cr.SetPVCShrinkState(sc.sts.Name, state)

	return nil
}

func (r *ReconcilePerconaServerMongoDB) shrinkStepdown(
	ctx context.Context,
	sc shrinkContext,
	state psmdbv1.PVCShrinkState,
) error {
	log := logf.FromContext(ctx).WithName("PVCShrink").WithValues("sts", sc.sts.Name, "phase", "stepdown")

	// NonVoting, Hidden, Mongos: skip stepdown (never primary)
	if !sc.needsStepdown || state.PrimaryPod == "" {
		log.Info("Skipping stepdown (component has no primary to step down)", "component", sc.component)
		state.Phase = "primary"
		sc.cr.SetPVCShrinkState(sc.sts.Name, state)
		return nil
	}

	// Find the primary pod
	pod := corev1.Pod{}
	if err := r.client.Get(ctx, client.ObjectKey{Name: state.PrimaryPod, Namespace: sc.cr.Namespace}, &pod); err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("Primary pod not found, skipping stepdown")
			state.Phase = "primary"
		} else {
			return errors.Wrapf(err, "get primary pod %s", state.PrimaryPod)
		}
	} else {
		isPrimary, err := r.isPodPrimary(ctx, sc.cr, pod, sc.replset)
		if err != nil {
			log.Info("Failed to check if pod is still primary, retrying", "pod", state.PrimaryPod, "error", err)
			return nil
		}
		if !isPrimary {
			log.Info("Primary already stepped down, advancing to primary phase", "pod", state.PrimaryPod)
		} else {
			log.Info("Stepping down primary", "pod", state.PrimaryPod)
			if err := r.stepDownPod(ctx, sc.cr, sc.replset, pod, 60); err != nil {
				return errors.Wrap(err, "step down primary for PVC shrink")
			}
		}
		state.Phase = "primary"
	}

	sc.cr.SetPVCShrinkState(sc.sts.Name, state)

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
		maps.Copy(pvc.Labels, pvcSpec.Labels)
		maps.Copy(pvc.Labels, sts.Labels)

		if pvc.Annotations == nil {
			pvc.Annotations = make(map[string]string)
		}
		maps.Copy(pvc.Annotations, pvcSpec.Annotations)

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
