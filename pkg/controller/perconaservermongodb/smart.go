package perconaservermongodb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

func (r *ReconcilePerconaServerMongoDB) smartUpdate(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	sfs *appsv1.StatefulSet,
	replset *api.ReplsetSpec,
	group membergroup.Group,
) error {
	log := logf.FromContext(ctx).
		WithName("SmartUpdate").
		WithValues("statefulset", sfs.Name, "replset", replset.Name, "group", group.Name)

	if group.Replicas == 0 {
		return nil
	}

	if cr.Spec.UpdateStrategy != api.SmartUpdateStatefulSetStrategyType {
		return nil
	}

	list := corev1.PodList{}
	if err := r.client.List(ctx,
		&list,
		&k8sclient.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(group.Labels),
		},
	); err != nil {
		return fmt.Errorf("get pod list: %v", err)
	}

	if !isSfsChanged(sfs, &list) {
		return nil
	}

	if cr.CompareVersion("1.4.0") < 0 {
		return nil
	}

	mongosFirst, err := r.shouldUpdateMongosFirst(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "should update mongos first")
	}
	if mongosFirst {
		return nil
	}

	if cr.Spec.Sharding.Enabled && sfs.Name != cr.Name+"-"+api.ConfigReplSetName {
		cfgSfs := appsv1.StatefulSet{}
		err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name + "-" + api.ConfigReplSetName, Namespace: cr.Namespace}, &cfgSfs)
		if err != nil {
			return errors.Wrapf(err, "get config statefulset %s/%s", cr.Namespace, cr.Name+"-"+api.ConfigReplSetName)
		}
		cfgList, err := psmdb.GetRSPods(ctx, r.client, cr, api.ConfigReplSetName)
		if err != nil {
			return errors.Wrap(err, "get cfg pod list")
		}
		if isSfsChanged(&cfgSfs, &cfgList) {
			log.Info("waiting for config RS update")
			return nil
		}
	}

	log.Info("StatefulSet is changed, starting smart update")

	if sfs.Status.ReadyReplicas < sfs.Status.Replicas {
		log.Info("can't start/continue 'SmartUpdate': waiting for all replicas are ready")
		return nil
	}

	// Disruption safety is a replica-set-level property. Separate PDBs and
	// StatefulSets give no combined MongoDB quorum guarantee, and a group-local
	// readiness check cannot see an unavailable voter in a sibling group.
	set, err := membergroup.Resolve(cr, replset)
	if err != nil {
		return errors.Wrapf(err, "resolve member groups for %s", replset.Name)
	}
	unavailable, err := r.replsetHasUnavailableVoters(ctx, cr, set)
	if err != nil {
		return errors.Wrap(err, "check replset voter availability")
	}
	if unavailable {
		log.Info("can't start/continue 'SmartUpdate': a voting member of this replica set is unavailable")
		return nil
	}

	waitLimit := int(group.LivenessProbe.InitialDelaySeconds)

	updatePod := func(pod *corev1.Pod) error {
		if err := r.applyNWait(ctx, cr, sfs.Status.UpdateRevision, pod, waitLimit); err != nil {
			return errors.Wrap(err, "failed to apply changes")
		}
		return nil
	}

	if rsStatus, ok := cr.Status.Replsets[replset.Name]; !ok || !rsStatus.Initialized {
		log.Info("replset wasn't initialized. Continuing smart update")

		for _, pod := range list.Items {
			log.Info("apply changes to pod", "pod", pod.Name)

			if err := updatePod(&pod); err != nil {
				return err
			}
		}

		log.Info("smart update finished for statefulset")

		return nil
	}

	if rsStatus, ok := cr.Status.Replsets[replset.Name]; ok && len(rsStatus.Members) > 0 {
		for _, pod := range list.Items {
			if _, ok := rsStatus.Members[pod.Name]; !ok {
				log.Info("pod is not a member of replset, updating it", "pod", pod.Name, "members", rsStatus.Members)

				if err := updatePod(&pod); err != nil {
					return err
				}

				return nil
			}
		}
	}

	isBackupRunning, err := r.isBackupRunning(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to check active backups")
	}
	if isBackupRunning {
		log.Info("can't start 'SmartUpdate': waiting for running backups to be finished")
		return nil
	}

	hasActiveJobs, err := backup.HasActiveJobs(ctx, r.newPBM, r.client, cr, backup.Job{}, backup.NotPITRLock)
	if err != nil {
		if cr.Status.State == api.AppStateError {
			log.Info("Failed to check active jobs. Proceeding with Smart Update because the cluster is in an error state", "error", err.Error())
		} else {
			return errors.Wrap(err, "failed to check active jobs")
		}
	}

	_, ok := sfs.Annotations[api.AnnotationRestoreInProgress]
	if !ok && hasActiveJobs {
		log.Info("can't start 'SmartUpdate': waiting for active jobs to be finished")
		return nil
	}

	if sfs.Name == cr.Name+"-"+api.ConfigReplSetName {
		err = r.disableBalancer(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "failed to stop balancer")
		}
	}

	util.SortPodsByOrdinalDesc(list.Items)

	var primaryPod corev1.Pod
	for _, pod := range list.Items {
		// Only ask a group that can hold data. An arbiter is never the primary,
		// and replicates no admin database for the probe to authenticate
		// against, so asking fails rather than answers.
		if group.DataBearing {
			isPrimary, err := r.isPodPrimary(ctx, cr, pod, replset)
			if err != nil {
				return errors.Wrap(err, "is pod primary")
			}
			if isPrimary {
				primaryPod = pod
				log.Info("primary pod detected", "pod", pod.Name)
				continue
			}
		}

		log.Info("apply changes to pod", "pod", pod.Name)

		if err := updatePod(&pod); err != nil {
			return err
		}
	}

	// Step down only when this group actually holds the live primary
	if len(primaryPod.Name) > 0 {
		forceStepDown := set.GetDataBearingVoterCount() == 1
		log.Info("doing step down...", "force", forceStepDown)
		client, err := r.mongoClientWithRole(ctx, cr, replset, api.RoleClusterAdmin)
		if err != nil {
			return fmt.Errorf("failed to get mongo client: %v", err)
		}

		defer func() {
			err := client.Disconnect(ctx)
			if err != nil {
				log.Error(err, "failed to close connection")
			}
		}()

		err = client.StepDown(ctx, 60, forceStepDown)
		if err != nil {
			if strings.Contains(err.Error(), "No electable secondaries caught up") {
				err = client.StepDown(ctx, 60, true)
				if err != nil {
					return errors.Wrap(err, "failed to do forced step down")
				}
			} else {
				return errors.Wrap(err, "failed to do step down")
			}
		}

		log.Info("apply changes to primary pod", "pod", primaryPod.Name)
		if err := updatePod(&primaryPod); err != nil {
			return err
		}
	}

	log.Info("smart update finished for statefulset")

	return nil
}

// replsetHasUnavailableVoters reports whether any voting member of the replica
// set is not ready, across every group.
//
// SmartUpdate must not disrupt a member while quorum is already at risk
// somewhere else in the replica set. PodDisruptionBudgets alone cannot express
// this: they are per-workload, and MongoDB quorum is per-replica-set.
func (r *ReconcilePerconaServerMongoDB) replsetHasUnavailableVoters(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	set *membergroup.Set,
) (bool, error) {
	for _, group := range set.GetAll() {
		if group.Member.Votes == 0 || group.Replicas == 0 {
			continue
		}

		sts := new(appsv1.StatefulSet)
		err := r.client.Get(ctx,
			types.NamespacedName{Name: group.STSName, Namespace: cr.Namespace}, sts)
		if k8sErrors.IsNotFound(err) {
			// A declared voting group with no workload yet: not available.
			return true, nil
		}
		if err != nil {
			return false, errors.Wrapf(err, "get statefulset %s", group.STSName)
		}

		if sts.Status.ReadyReplicas < group.Replicas {
			return true, nil
		}
	}

	return false, nil
}

func (r *ReconcilePerconaServerMongoDB) shouldUpdateMongosFirst(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	if !cr.Spec.Sharding.Enabled {
		return false, nil
	}

	c := new(api.PerconaServerMongoDB)
	if err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, c); err != nil {
		return false, errors.Wrap(err, "failed to get cr")
	}

	_, ok := c.Annotations[api.AnnotationUpdateMongosFirst]
	return ok, nil
}

func (r *ReconcilePerconaServerMongoDB) setUpdateMongosFirst(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if !cr.Spec.Sharding.Enabled {
		return nil
	}

	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		c := new(api.PerconaServerMongoDB)
		if err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, c); err != nil {
			return err
		}
		if c.Annotations == nil {
			c.Annotations = make(map[string]string)
		}
		c.Annotations[api.AnnotationUpdateMongosFirst] = "true"

		return r.client.Update(ctx, c)
	})
}

func (r *ReconcilePerconaServerMongoDB) unsetUpdateMongosFirst(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if !cr.Spec.Sharding.Enabled {
		return nil
	}
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		c := new(api.PerconaServerMongoDB)
		if err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, c); err != nil {
			return err
		}
		if _, ok := c.Annotations[api.AnnotationUpdateMongosFirst]; !ok {
			return nil
		}

		delete(c.Annotations, api.AnnotationUpdateMongosFirst)

		return r.client.Update(ctx, c)
	})
}

func (r *ReconcilePerconaServerMongoDB) setPrimary(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	rs *api.ReplsetSpec,
	set *membergroup.Set,
	expectedPrimary corev1.Pod,
) error {
	primary, err := r.isPodPrimary(ctx, cr, expectedPrimary, rs)
	if err != nil {
		return errors.Wrap(err, "is pod primary")
	}
	if primary {
		return nil
	}

	// The replacement must be selected from the whole replica set: the observed
	// primary can live in any group, and so can the healthy, caught-up member
	// we want to hand the primary to.
	pods, err := psmdb.GetOutdatedRSPods(ctx, r.client, cr, rs.Name)
	if err != nil {
		return errors.Wrap(err, "get replset pods")
	}

	sleepSeconds := int(*rs.TerminationGracePeriodSeconds) * len(pods.Items)

	candidates := make([]corev1.Pod, 0, len(pods.Items)) // these pods will be fronzen
	var primaryPod corev1.Pod                            // this holds the current primary pod

	for i := range pods.Items {
		pod := pods.Items[i]
		if expectedPrimary.Name == pod.Name {
			continue
		}

		// Arbiters cannot be frozen or stepped down, and cannot even be
		// connected to as clusterAdmin.
		if isArbiterPod(&pod, set) {
			continue
		}

		isPrimary, err := r.isPodPrimary(ctx, cr, pod, rs)
		if err != nil {
			return errors.Wrap(err, "is pod primary")
		}
		if isPrimary {
			primaryPod = pod
			continue
		}

		candidates = append(candidates, pod)
	}

	if primaryPod.Name == "" {
		logf.FromContext(ctx).Info(
			"no primary to step down, leaving members electable",
			"replset", rs.Name, "expectedPrimary", expectedPrimary.Name)
		return nil
	}

	// Freeze every other candidate first, so the step down below can only be
	// won by expectedPrimary.
	for i := range candidates {
		if err := r.freezePod(ctx, cr, rs, candidates[i], sleepSeconds); err != nil {
			return errors.Wrapf(err, "failed to freeze %s pod", candidates[i].Name)
		}
	}

	if err := r.stepDownPod(ctx, cr, rs, primaryPod, sleepSeconds); err != nil {
		return errors.Wrap(err, "failed to step down primary pod")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) stepDownPod(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, pod corev1.Pod, seconds int) error {
	log := logf.FromContext(ctx)

	mgoClient, err := r.standaloneClientWithRole(ctx, cr, rs, api.RoleClusterAdmin, pod)
	if err != nil {
		return errors.Wrap(err, "failed to create standalone client")
	}
	defer func() {
		err := mgoClient.Disconnect(ctx)
		if err != nil {
			log.Error(err, "failed to close connection")
		}
	}()
	if err := mgoClient.StepDown(ctx, seconds, false); err != nil {
		return errors.Wrap(err, "failed to step down")
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) freezePod(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, pod corev1.Pod, seconds int) error {
	log := logf.FromContext(ctx)

	mgoClient, err := r.standaloneClientWithRole(ctx, cr, rs, api.RoleClusterAdmin, pod)
	if err != nil {
		return errors.Wrap(err, "failed to create standalone client")
	}
	defer func() {
		err := mgoClient.Disconnect(ctx)
		if err != nil {
			log.Error(err, "failed to close connection")
		}
	}()
	if err := mgoClient.Freeze(ctx, seconds); err != nil {
		return errors.Wrap(err, "failed to freeze")
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) isPodPrimary(ctx context.Context, cr *api.PerconaServerMongoDB, pod corev1.Pod, rs *api.ReplsetSpec) (bool, error) {
	log := logf.FromContext(ctx)

	mgoClient, err := r.standaloneClientWithRole(ctx, cr, rs, api.RoleClusterAdmin, pod)
	if err != nil {
		return false, errors.Wrap(err, "failed to create standalone client")
	}
	defer func() {
		err := mgoClient.Disconnect(ctx)
		if err != nil {
			log.Error(err, "failed to close connection")
		}
	}()

	isMaster, err := mgoClient.IsMaster(ctx)
	if err != nil {
		return false, errors.Wrap(err, "is master")
	}

	return isMaster.IsMaster, nil
}

func (r *ReconcilePerconaServerMongoDB) smartMongosUpdate(ctx context.Context, cr *api.PerconaServerMongoDB, sts *appsv1.StatefulSet) error {
	log := logf.FromContext(ctx)

	if cr.Spec.Sharding.Mongos.Size == 0 || cr.Spec.UpdateStrategy != api.SmartUpdateStatefulSetStrategyType {
		return nil
	}

	list, err := r.getMongosPods(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "get mongos pods")
	}

	if !isSfsChanged(sts, &list) {
		return nil
	}

	log.Info("StatefulSet is changed, starting smart update", "name", sts.Name)

	if sts.Status.ReadyReplicas < sts.Status.Replicas {
		log.Info("can't start/continue 'SmartUpdate': waiting for all replicas are ready")
		return nil
	}

	isBackupRunning, err := r.isBackupRunning(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to check active backups")
	}
	if isBackupRunning {
		log.Info("can't start 'SmartUpdate': waiting for running backups to be finished")
		return nil
	}

	hasActiveJobs, err := backup.HasActiveJobs(ctx, r.newPBM, r.client, cr, backup.Job{}, backup.NotPITRLock)
	if err != nil {
		return errors.Wrap(err, "failed to check active jobs")
	}

	if hasActiveJobs {
		log.Info("can't start 'SmartUpdate': waiting for active jobs to be finished")
		return nil
	}

	waitLimit := int(cr.Spec.Sharding.Mongos.LivenessProbe.InitialDelaySeconds)

	// Roll out pods in ascending ordinal order (pod-0 -> pod-N).
	//
	// The order doesn't matter for mongos itself, but it does matter right after the
	// mongos StatefulSet is re-created with orphaned pods: reconcileMongosStatefulset
	// deletes and re-creates it whenever the log volumeClaimTemplate is added or removed,
	// because volumeClaimTemplates are immutable.
	//
	// The adopted pods still run the old spec, so on every sync the StatefulSet controller
	// walks them in ascending order and tries to patch the first pod whose volumes don't
	// match the new template. That patch is always rejected, since a running pod's volumes
	// can't be changed, and the sync aborts there without touching any higher ordinal.
	//
	// Descending order would deadlock: pod-N is deleted, but the controller keeps failing
	// on pod-0 and never re-creates pod-N. Ascending order keeps the pod we delete the same
	// pod the controller is stuck on, so every deletion lets it advance one ordinal.
	util.SortPodsByOrdinalAsc(list.Items)

	for _, pod := range list.Items {
		if err := r.applyNWait(ctx, cr, sts.Status.UpdateRevision, &pod, waitLimit); err != nil {
			return errors.Wrap(err, "failed to apply changes")
		}
	}
	if err := r.unsetUpdateMongosFirst(ctx, cr); err != nil {
		return errors.Wrap(err, "unset update mongos first")
	}
	log.Info("smart update finished for mongos statefulset")

	return nil
}

func (r *ReconcilePerconaServerMongoDB) isStsListUpToDate(ctx context.Context, cr *api.PerconaServerMongoDB, stsList *appsv1.StatefulSetList) (bool, error) {
	for _, s := range stsList.Items {
		podList := new(corev1.PodList)
		if err := r.client.List(ctx, podList,
			&k8sclient.ListOptions{
				Namespace:     cr.Namespace,
				LabelSelector: labels.SelectorFromSet(s.Labels),
			}); err != nil {
			return false, errors.Errorf("failed to get statefulset %s pods: %v", s.Name, err)
		}
		if s.Status.UpdatedReplicas < s.Status.Replicas || isSfsChanged(&s, podList) {
			logf.FromContext(ctx).Info("StatefulSet is not up to date", "sts", s.Name)
			return false, nil
		}
	}
	return true, nil
}

func (r *ReconcilePerconaServerMongoDB) isAllSfsUpToDate(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	sfsList := appsv1.StatefulSetList{}
	if err := r.client.List(ctx, &sfsList,
		&k8sclient.ListOptions{
			Namespace: cr.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				naming.LabelKubernetesInstance: cr.Name,
			}),
		},
	); err != nil {
		return false, errors.Wrap(err, "failed to get statefulset list")
	}

	return r.isStsListUpToDate(ctx, cr, &sfsList)
}

func (r *ReconcilePerconaServerMongoDB) applyNWait(ctx context.Context, cr *api.PerconaServerMongoDB, updateRevision string, pod *corev1.Pod, waitLimit int) error {
	if pod.ObjectMeta.Labels["controller-revision-hash"] == updateRevision {
		logf.FromContext(ctx).Info("Pod already updated", "pod", pod.Name)
	} else {
		if err := r.client.Delete(ctx, pod); err != nil {
			return errors.Wrap(err, "delete pod")
		}
	}

	if err := r.waitPodRestart(ctx, cr, updateRevision, pod, waitLimit); err != nil {
		return errors.Wrap(err, "wait pod restart")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) waitPodRestart(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	updateRevision string,
	pod *corev1.Pod,
	waitLimit int,
) error {
	for range waitLimit {
		time.Sleep(time.Second * 1)

		err := r.client.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, pod)
		if err != nil && !k8sErrors.IsNotFound(err) {
			return errors.Wrap(err, "get pod")
		}

		// We update status in every loop to not wait until the end of smart update
		if err := r.updateStatus(ctx, cr, nil, api.AppStateInit); err != nil {
			return errors.Wrap(err, "update status")
		}

		ready := false
		for _, container := range pod.Status.ContainerStatuses {
			if isMemberContainer(container.Name) {
				ready = container.Ready
			}
		}

		if pod.Status.Phase == corev1.PodRunning && pod.ObjectMeta.Labels["controller-revision-hash"] == updateRevision && ready {
			logf.FromContext(ctx).Info("Pod started", "pod", pod.Name)
			return nil
		}
	}

	return errors.New("reach pod wait limit")
}

// isArbiterPod returns true if the pod contains an arbiter container
func isArbiterPod(pod *corev1.Pod, set *membergroup.Set) bool {
	if group, ok := set.GetByLabels(pod.Labels); ok {
		return !group.DataBearing
	}

	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == naming.ContainerArbiter {
			return true
		}
	}

	return false
}

func isMemberContainer(name string) bool {
	switch name {
	case naming.ContainerMongod, naming.ContainerMongos,
		naming.ContainerNonVoting, naming.ContainerArbiter, naming.ContainerHidden:
		return true
	}
	return false
}

func isSfsChanged(sfs *appsv1.StatefulSet, podList *corev1.PodList) bool {
	if sfs.Status.UpdateRevision == "" {
		return false
	}

	for _, pod := range podList.Items {
		if pod.Labels[naming.LabelKubernetesComponent] != sfs.Labels[naming.LabelKubernetesComponent] {
			continue
		}
		if pod.ObjectMeta.Labels["controller-revision-hash"] != sfs.Status.UpdateRevision {
			return true
		}
	}
	return false
}
