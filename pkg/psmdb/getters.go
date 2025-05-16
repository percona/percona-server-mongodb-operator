package psmdb

import (
	"context"
	"sort"
	"time"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/mcs"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
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

	stsList := appsv1.StatefulSetList{} // All statefulsets related to replset `rsName`
	if err := k8sclient.List(ctx, &stsList,
		&client.ListOptions{
			Namespace: cr.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				naming.LabelKubernetesInstance: cr.Name,
				naming.LabelKubernetesReplset:  rsName,
			}),
		},
	); err != nil {
		return rsPods, errors.Wrapf(err, "failed to get statefulset list related to replset %s", rsName)
	}

	for _, sts := range stsList.Items {
		rs := cr.Spec.Replset(rsName)

		lbls := naming.RSLabels(cr, rs)
		lbls[naming.LabelKubernetesComponent] = sts.Labels[naming.LabelKubernetesComponent]
		pods := corev1.PodList{}
		err := k8sclient.List(ctx,
			&pods,
			&client.ListOptions{
				Namespace:     cr.Namespace,
				LabelSelector: labels.SelectorFromSet(lbls),
			},
		)
		if err != nil {
			return rsPods, errors.Wrap(err, "failed to list pods")
		}

		if trimOutdated && rs != nil {
			// `k8sclient.List` returns unsorted list of pods
			// We should sort pods to truncate pods that are going to be deleted during resize
			// More info: https://github.com/percona/percona-server-mongodb-operator/pull/1323#issue-1904904799
			sort.Slice(pods.Items, func(i, j int) bool {
				return pods.Items[i].Name < pods.Items[j].Name
			})

			// We can't use `sts.Spec.Replicas` because it can be different from `rs.Size`.
			// This will lead to inserting pods, which are going to be deleted, to the
			// `replSetReconfig` call in the `updateConfigMembers` function.
			rsSize := 0

			switch lbls[naming.LabelKubernetesComponent] {
			case naming.ComponentArbiter:
				rsSize = int(rs.Arbiter.Size)
			case naming.ComponentNonVoting:
				rsSize = int(rs.NonVoting.Size)
			case naming.ComponentHidden:
				rsSize = int(rs.Hidden.Size)
			default:
				rsSize = int(rs.Size)
			}
			if len(pods.Items) >= rsSize {
				pods.Items = pods.Items[:rsSize]
			}
		}

		rsPods.Items = append(rsPods.Items, pods.Items...)
	}

	return rsPods, nil
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
