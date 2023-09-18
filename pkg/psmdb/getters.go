package psmdb

import (
	"context"

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

func GetRSPods(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, rsName string) (corev1.PodList, error) {
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
		compPods := corev1.PodList{}
		err := k8sclient.List(ctx,
			&compPods,
			&client.ListOptions{
				Namespace:     cr.Namespace,
				LabelSelector: labels.SelectorFromSet(lbls),
			},
		)
		if err != nil {
			return rsPods, errors.Wrap(err, "failed to list pods")
		}

		rsPods.Items = append(rsPods.Items, compPods.Items...)
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
