package perconaservermongodb

import (
	"context"
	"fmt"
	"sort"
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
)

func (r *ReconcilePerconaServerMongoDB) smartUpdate(ctx context.Context, cr *api.PerconaServerMongoDB, sfs *appsv1.StatefulSet,
	replset *api.ReplsetSpec,
) error {
	log := logf.FromContext(ctx).WithName("SmartUpdate").WithValues("statefulset", sfs.Name, "replset", replset.Name)

	if replset.Size == 0 {
		return nil
	}

	if cr.Spec.UpdateStrategy != api.SmartUpdateStatefulSetStrategyType {
		return nil
	}

	matchLabels := naming.RSLabels(cr, replset)

	label, ok := sfs.Labels[naming.LabelKubernetesComponent]
	if ok {
		matchLabels[naming.LabelKubernetesComponent] = label
	}

	list := corev1.PodList{}
	if err := r.client.List(ctx,
		&list,
		&k8sclient.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(matchLabels),
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

	waitLimit := int(replset.LivenessProbe.InitialDelaySeconds)

	updatePod := func(pod *corev1.Pod) error {
		updateRevision := sfs.Status.UpdateRevision
		if pod.Labels[naming.LabelKubernetesComponent] == "arbiter" {
			arbiterSfs, err := r.getArbiterStatefulset(ctx, cr, replset)
			if err != nil {
				return errors.Wrap(err, "failed to get arbiter statefulset")
			}

			updateRevision = arbiterSfs.Status.UpdateRevision
		}

		if err := r.applyNWait(ctx, cr, updateRevision, pod, waitLimit); err != nil {
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

	if rsStatus, ok := cr.Status.Replsets[replset.Name]; ok && rsStatus.Members != nil {
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

	_, ok = sfs.Annotations[api.AnnotationRestoreInProgress]
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

	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name > list.Items[j].Name
	})

	var primaryPod corev1.Pod
	for _, pod := range list.Items {
		isPrimary, err := r.isPodPrimary(ctx, cr, pod, replset)
		if err != nil {
			return errors.Wrap(err, "is pod primary")
		}
		if isPrimary {
			primaryPod = pod
			log.Info("primary pod detected", "pod", pod.Name)
			continue
		}

		log.Info("apply changes to secondary pod", "pod", pod.Name)

		if err := updatePod(&pod); err != nil {
			return err
		}
	}

	component := sfs.Labels[naming.LabelKubernetesComponent]
	// Primary can't be one of NonVoting and Hidden members, so we don't need to step down
	// If the primary is external, we can't match it with a running pod and it'll have an empty name
	if component != naming.ComponentNonVoting && component != naming.ComponentHidden && len(primaryPod.Name) > 0 {
		forceStepDown := replset.Size == 1
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

func (r *ReconcilePerconaServerMongoDB) setPrimary(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, expectedPrimary corev1.Pod) error {
	primary, err := r.isPodPrimary(ctx, cr, expectedPrimary, rs)
	if err != nil {
		return errors.Wrap(err, "is pod primary")
	}
	if primary {
		return nil
	}

	sts, err := r.getRsStatefulset(ctx, cr, rs.Name)
	if err != nil {
		return errors.Wrap(err, "get rs statefulset")
	}
	pods := &corev1.PodList{}
	err = r.client.List(ctx,
		pods,
		&k8sclient.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(sts.Spec.Template.Labels),
		},
	)
	if err != nil {
		return errors.Wrap(err, "get rs statefulset")
	}

	sleepSeconds := int(*rs.TerminationGracePeriodSeconds) * len(pods.Items)

	var primaryPod corev1.Pod
	for _, pod := range pods.Items {
		if expectedPrimary.Name == pod.Name {
			continue
		}
		primary, err := r.isPodPrimary(ctx, cr, pod, rs)
		if err != nil {
			return errors.Wrap(err, "is pod primary")
		}
		// If we found a primary, we need to call `replSetStepDown` on it after calling `replSetFreeze` on all other pods
		if primary {
			primaryPod = pod
			continue
		}
		err = r.freezePod(ctx, cr, rs, pod, sleepSeconds)
		if err != nil {
			return errors.Wrapf(err, "failed to freeze %s pod", pod.Name)
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

	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name > list.Items[j].Name
	})

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

func (r *ReconcilePerconaServerMongoDB) waitPodRestart(ctx context.Context, cr *api.PerconaServerMongoDB, updateRevision string, pod *corev1.Pod, waitLimit int) error {
	for i := 0; i < waitLimit; i++ {
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
			switch container.Name {
			case naming.ContainerMongod, naming.ContainerMongos, naming.ContainerNonVoting, naming.ContainerArbiter, naming.ContainerHidden:
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
