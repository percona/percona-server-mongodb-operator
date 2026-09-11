package perconaservermongodb

import (
	"context"
	"sort"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
)

// memberReplsets returns every replica set that owns member workloads: the
// shards plus the config server.
//
// The config server is included regardless of spec.sharding.enabled, because
// its pods and volumes outlive the moment sharding is switched off.
func memberReplsets(cr *api.PerconaServerMongoDB) []*api.ReplsetSpec {
	repls := make([]*api.ReplsetSpec, 0, len(cr.Spec.Replsets)+1)
	if cr.Spec.Sharding.ConfigsvrReplSet != nil {
		repls = append(repls, cr.Spec.Sharding.ConfigsvrReplSet)
	}
	for _, rs := range cr.Spec.Replsets {
		if rs != nil {
			repls = append(repls, rs)
		}
	}
	return repls
}

// getMemberPods returns the pods of every member group of every replica set,
// the config server included.
func (r *ReconcilePerconaServerMongoDB) getMemberPods(ctx context.Context, cr *api.PerconaServerMongoDB) (corev1.PodList, error) {
	list := corev1.PodList{}

	for _, rs := range memberReplsets(cr) {
		pods, err := psmdb.GetOutdatedRSPods(ctx, r.client, cr, rs.Name)
		if err != nil {
			return list, errors.Wrapf(err, "get pods of replset %s", rs.Name)
		}

		list.Items = append(list.Items, pods.Items...)
	}

	return list, nil
}

func (r *ReconcilePerconaServerMongoDB) getMongosPods(ctx context.Context, cr *api.PerconaServerMongoDB) (corev1.PodList, error) {
	mongosPods := corev1.PodList{}
	err := r.client.List(ctx,
		&mongosPods,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(naming.MongosLabels(cr)),
		},
	)

	return mongosPods, err
}

// getShardsWithWorkloads returns the names of the shard replica sets that
// still have member workloads in the cluster. The config server, mongos and
// search are excluded.
func (r *ReconcilePerconaServerMongoDB) getShardsWithWorkloads(ctx context.Context, cr *api.PerconaServerMongoDB) (map[string]struct{}, error) {
	list := appsv1.StatefulSetList{}

	if err := r.client.List(ctx,
		&list,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(naming.ClusterLabels(cr)),
		},
	); err != nil {
		return nil, errors.Wrap(err, "list statefulsets")
	}

	names := make(map[string]struct{}, len(list.Items))
	for i := range list.Items {
		sts := &list.Items[i]

		switch sts.Labels[naming.LabelKubernetesComponent] {
		case naming.ComponentMongos, naming.ComponentSearch:
			continue
		}

		if !metav1.IsControlledBy(sts, cr) {
			continue
		}

		name := sts.Labels[naming.LabelKubernetesReplset]
		if name == "" || name == api.ConfigReplSetName {
			continue
		}

		names[name] = struct{}{}
	}

	return names, nil
}

func (r *ReconcilePerconaServerMongoDB) getStatefulsetsExceptMongos(ctx context.Context, cr *api.PerconaServerMongoDB) (appsv1.StatefulSetList, error) {
	list := appsv1.StatefulSetList{}

	selectors := labels.SelectorFromSet(naming.ClusterLabels(cr))

	req, err := labels.NewRequirement(naming.LabelKubernetesComponent, selection.NotEquals, []string{"mongos"})
	if err != nil {
		return list, errors.Wrap(err, "get selector requirement")
	}
	selectors = selectors.Add(*req)

	err = r.client.List(ctx,
		&list,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: selectors,
		},
	)

	return list, err
}

func (r *ReconcilePerconaServerMongoDB) getAllstatefulsets(ctx context.Context, cr *api.PerconaServerMongoDB) (appsv1.StatefulSetList, error) {
	list := appsv1.StatefulSetList{}
	filteredList := appsv1.StatefulSetList{}

	err := r.client.List(ctx,
		&list,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(naming.ClusterLabels(cr)),
		},
	)

	for _, sts := range list.Items {
		if metav1.IsControlledBy(&sts, cr) {
			filteredList.Items = append(filteredList.Items, sts)
		}
	}

	return filteredList, err
}

func (r *ReconcilePerconaServerMongoDB) getCfgStatefulset(ctx context.Context, cr *api.PerconaServerMongoDB) (appsv1.StatefulSet, error) {
	sts := appsv1.StatefulSet{}
	err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name + "-" + api.ConfigReplSetName, Namespace: cr.Namespace}, &sts)
	return sts, err
}

func (r *ReconcilePerconaServerMongoDB) getAllPVCs(ctx context.Context, cr *api.PerconaServerMongoDB) (corev1.PersistentVolumeClaimList, error) {
	list := corev1.PersistentVolumeClaimList{}

	err := r.client.List(ctx,
		&list,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(naming.ClusterLabels(cr)),
		},
	)

	return list, err
}

// getMemberPVCs returns the persistent volume claims of every member group of every replica set.
func (r *ReconcilePerconaServerMongoDB) getMemberPVCs(ctx context.Context, cr *api.PerconaServerMongoDB) (corev1.PersistentVolumeClaimList, error) {
	list := corev1.PersistentVolumeClaimList{}

	notSearch, err := labels.NewRequirement(naming.LabelKubernetesComponent, selection.NotEquals, []string{naming.ComponentSearch})
	if err != nil {
		return list, errors.Wrap(err, "get selector requirement")
	}

	for _, rs := range memberReplsets(cr) {
		pvcs := corev1.PersistentVolumeClaimList{}
		if err := r.client.List(ctx,
			&pvcs,
			&client.ListOptions{
				Namespace:     cr.Namespace,
				LabelSelector: labels.SelectorFromSet(naming.RSLabels(cr, rs)).Add(*notSearch),
			},
		); err != nil {
			return list, errors.Wrapf(err, "get pvcs of replset %s", rs.Name)
		}

		list.Items = append(list.Items, pvcs.Items...)
	}

	return list, nil
}

// getMemberStatefulsets returns every member workload of a replica set: all
// groups, including retiring ones. mongos and search are excluded — they are
// not members and must never enter member counts or member cleanup.
//
// Filtering is by owner reference as well as cluster and replica set identity,
// so a foreign object that happens to carry matching labels is ignored.
func (r *ReconcilePerconaServerMongoDB) getMemberStatefulsets(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	rs *api.ReplsetSpec,
) (appsv1.StatefulSetList, error) {
	list := appsv1.StatefulSetList{}
	out := appsv1.StatefulSetList{}

	sel := labels.SelectorFromSet(map[string]string{
		naming.LabelKubernetesInstance: cr.Name,
		naming.LabelKubernetesReplset:  rs.Name,
	})

	if err := r.client.List(ctx, &list, &client.ListOptions{
		Namespace:     cr.Namespace,
		LabelSelector: sel,
	}); err != nil {
		return out, errors.Wrapf(err, "list statefulsets for replset %s", rs.Name)
	}

	for i := range list.Items {
		sts := &list.Items[i]
		switch sts.Labels[naming.LabelKubernetesComponent] {
		case naming.ComponentMongos, naming.ComponentSearch:
			continue
		}
		if !metav1.IsControlledBy(sts, cr) {
			continue
		}
		out.Items = append(out.Items, *sts)
	}

	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Name < out.Items[j].Name })

	return out, nil
}

func (r *ReconcilePerconaServerMongoDB) getGroupStatefulset(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	rs *api.ReplsetSpec,
	group membergroup.Group,
) (*appsv1.StatefulSet, error) {
	sts := new(appsv1.StatefulSet)
	err := r.client.Get(ctx,
		types.NamespacedName{Name: group.STSName, Namespace: cr.Namespace}, sts)
	return sts, err
}

// getEligibleMemberPod returns a pod suitable for a specific operation, chosen
// across every group of the replica set.
//
// Selection is by the predicate.
func (r *ReconcilePerconaServerMongoDB) getEligibleMemberPod(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	rs *api.ReplsetSpec,
	set *membergroup.Set,
	eligible func(membergroup.Group, *corev1.Pod) bool,
) (*corev1.Pod, *membergroup.Group, error) {
	for _, group := range set.GetAll() {
		if group.Replicas == 0 {
			continue
		}

		pods, err := psmdb.GetGroupPods(ctx, r.client, cr, rs, group)
		if err != nil {
			return nil, nil, err
		}

		for i := range pods.Items {
			pod := &pods.Items[i]
			if ordinal, ok := naming.PodOrdinal(pod.Name); !ok || ordinal >= int(group.Replicas) {
				continue
			}
			if eligible(group, pod) {
				return pod, &group, nil
			}
		}
	}

	return nil, nil, errors.Errorf("no eligible member pod in replset %s", rs.Name)
}

func IsReadyDataBearingPod(group membergroup.Group, pod *corev1.Pod) bool {
	return group.DataBearing &&
		isContainerAndPodRunning(*pod, group.ContainerName) &&
		isPodReady(*pod)
}
