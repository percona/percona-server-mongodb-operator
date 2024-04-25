package psmdb

import (
	"context"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/mcs"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
)

func clusterLabels(cr *api.PerconaServerMongoDB) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "percona-server-mongodb",
		"app.kubernetes.io/instance":   cr.Name,
		"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
		"app.kubernetes.io/part-of":    "percona-server-mongodb",
	}
}

func RSLabels(cr *api.PerconaServerMongoDB, rsName string) map[string]string {
	lbls := clusterLabels(cr)
	lbls["app.kubernetes.io/replset"] = rsName
	return lbls
}

func MongosLabels(cr *api.PerconaServerMongoDB) map[string]string {
	lbls := clusterLabels(cr)
	lbls["app.kubernetes.io/component"] = "mongos"
	return lbls
}

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
				"app.kubernetes.io/instance": cr.Name,
				"app.kubernetes.io/replset":  rsName,
			}),
		},
	); err != nil {
		return rsPods, errors.Wrapf(err, "failed to get statefulset list related to replset %s", rsName)
	}

	for _, sts := range stsList.Items {
		lbls := RSLabels(cr, rsName)
		lbls["app.kubernetes.io/component"] = sts.Labels["app.kubernetes.io/component"]
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

		rs := cr.Spec.Replset(rsName)
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

			switch lbls["app.kubernetes.io/component"] {
			case "arbiter":
				rsSize = int(rs.Arbiter.Size)
			case "nonVoting":
				rsSize = int(rs.NonVoting.Size)
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

func GetMongosPods(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) (corev1.PodList, error) {
	pods := corev1.PodList{}
	err := cl.List(ctx,
		&pods,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(MongosLabels(cr)),
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
			LabelSelector: labels.SelectorFromSet(MongosLabels(cr)),
		},
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list mongos services")
	}
	return list, nil
}

func GetExportedServices(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) (*mcsv1alpha1.ServiceExportList, error) {
	ls := clusterLabels(cr)

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
