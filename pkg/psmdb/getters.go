package psmdb

import (
	"context"
	"sort"
	"time"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/mcs"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

// GetRSPods returns truncated list of replicaset pods to the size of `rs.Size`.
func GetRSPods(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, rsName string) (corev1.PodList, error) {
	return getRSPods(ctx, k8sclient, cr, rsName, true)
}

// GetOutdatedRSPods does the same as GetRSPods but doesn't truncate the list of pods
func GetOutdatedRSPods(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, rsName string) (corev1.PodList, error) {
	return getRSPods(ctx, k8sclient, cr, rsName, false)
}

func getRSPods(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, rsName string, trimOutdated bool) (corev1.PodList, error) {
	rsPods := corev1.PodList{}

	selectors := labels.SelectorFromSet(map[string]string{
		naming.LabelKubernetesInstance: cr.Name,
		naming.LabelKubernetesReplset:  rsName,
	})

	// All statefulsets related to replset `rsName` except component=search
	req, err := labels.NewRequirement(naming.LabelKubernetesComponent, selection.NotEquals, []string{naming.ComponentSearch})
	if err != nil {
		return rsPods, errors.Wrap(err, "get selector requirement")
	}
	selectors = selectors.Add(*req)

	stsList := appsv1.StatefulSetList{}
	if err := k8sclient.List(ctx, &stsList, &client.ListOptions{
		Namespace:     cr.Namespace,
		LabelSelector: selectors,
	}); err != nil {
		return rsPods, errors.Wrapf(err, "failed to get statefulset list related to replset %s", rsName)
	}

	// `client.List` doesn't guarantee ordering (the cache-backed client returns
	// items in map iteration order). Sort StatefulSets by name so iteration
	// order is deterministic.
	sort.Slice(stsList.Items, func(i, j int) bool {
		return stsList.Items[i].Name < stsList.Items[j].Name
	})

	rs := cr.Spec.Replset(rsName)

	// A desired-group view. A StatefulSet whose group is no longer declared
	// resolves to nothing: its pods are still returned in the untruncated view
	// so cleanup, connectivity and termination checks can see them, but they
	// never enter the desired membership.
	var set *membergroup.Set
	if rs != nil {
		set, err = membergroup.Resolve(cr, rs)
		if err != nil {
			return rsPods, errors.Wrapf(err, "resolve member groups for replset %s", rsName)
		}
	}

	for i := range stsList.Items {
		sts := &stsList.Items[i]

		lbls := naming.RSLabels(cr, rs)
		lbls[naming.LabelKubernetesComponent] = sts.Labels[naming.LabelKubernetesComponent]

		pods := corev1.PodList{}
		if err := k8sclient.List(ctx, &pods, &client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(lbls),
		}); err != nil {
			return rsPods, errors.Wrap(err, "failed to list pods")
		}

		util.SortPodsByOrdinalAsc(pods.Items)

		if trimOutdated {
			group, ok := groupForSTS(set, sts)
			if !ok {
				// Retiring or unknown workload: it contributes no desired
				// members. Never fall back to the main group.
				continue
			}
			pods.Items = selectDesiredOrdinals(pods.Items, group.Replicas)
		}

		rsPods.Items = append(rsPods.Items, pods.Items...)
	}

	return rsPods, nil
}

func groupForSTS(set *membergroup.Set, sts *appsv1.StatefulSet) (membergroup.Group, bool) {
	if set == nil {
		return membergroup.Group{}, false
	}
	return set.GetByLabels(sts.Labels)
}

// selectDesiredOrdinals keeps the pods whose ordinal is inside [0, replicas).
//
// We can't use sts.Spec.Replicas because it can differ from the desired count
// during a resize. Including a pod that is about to be deleted would insert it
// into the replSetReconfig call in updateConfigMembers.
func selectDesiredOrdinals(pods []corev1.Pod, replicas int32) []corev1.Pod {
	kept := make([]corev1.Pod, 0, len(pods))
	for i := range pods {
		ordinal, ok := naming.PodOrdinal(pods[i].Name)
		if !ok || ordinal >= int(replicas) {
			continue
		}
		kept = append(kept, pods[i])
	}
	return kept
}

func GetPrimaryPod(ctx context.Context, mgoClient mongo.Client) (string, error) {
	status, err := mgoClient.RSStatus(ctx)
	if err != nil {
		return "", errors.Wrap(err, "failed to get rs status")
	}

	return status.Primary().Name, nil
}

func GetMongosSts(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) (*appsv1.StatefulSet, error) {
	sts := MongosStatefulset(cr)
	err := cl.Get(ctx, client.ObjectKeyFromObject(sts), sts)
	return sts, err
}

func GetMongosPods(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) (corev1.PodList, error) {
	pods := corev1.PodList{}
	err := cl.List(ctx,
		&pods,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(naming.MongosLabels(cr)),
		},
	)

	return pods, err
}

func GetMongosServices(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) (*corev1.ServiceList, error) {
	list := new(corev1.ServiceList)
	err := cl.List(ctx,
		list,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(naming.MongosLabels(cr)),
		},
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list mongos services")
	}
	return list, nil
}

func GetExportedServices(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) (*mcsv1alpha1.ServiceExportList, error) {
	ls := naming.ClusterLabels(cr)

	seList := mcs.ServiceExportList()
	err := cl.List(ctx,
		seList,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(ls),
		},
	)
	if err != nil {
		return nil, errors.Wrap(err, "get service export list")
	}

	return seList, nil
}

func GetNodeLabels(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, pod corev1.Pod) (map[string]string, error) {
	// Set a timeout for the request, to avoid hanging forever
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	node := &corev1.Node{}

	err := cl.Get(ctx, client.ObjectKey{Name: pod.Spec.NodeName}, node)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get node %s", pod.Spec.NodeName)
	}

	return node.Labels, nil
}

// GetGroupPods returns the observed pods of one group, sorted by ordinal and
// not truncated.
func GetGroupPods(
	ctx context.Context,
	cl client.Client,
	cr *api.PerconaServerMongoDB,
	rs *api.ReplsetSpec,
	group membergroup.Group,
) (corev1.PodList, error) {
	pods := corev1.PodList{}
	if err := cl.List(ctx, &pods, &client.ListOptions{
		Namespace:     cr.Namespace,
		LabelSelector: labels.SelectorFromSet(group.Labels),
	}); err != nil {
		return pods, errors.Wrapf(err, "list pods for group %s", group.Name)
	}
	util.SortPodsByOrdinalAsc(pods.Items)
	return pods, nil
}
