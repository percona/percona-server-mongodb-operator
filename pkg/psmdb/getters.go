package psmdb

import (
	"context"
	"sort"

	mgo "go.mongodb.org/mongo-driver/mongo"
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

func rsLabels(cr *api.PerconaServerMongoDB, rsName string) map[string]string {
	lbls := clusterLabels(cr)
	lbls["app.kubernetes.io/replset"] = rsName
	return lbls
}

func mongosLabels(cr *api.PerconaServerMongoDB) map[string]string {
	lbls := clusterLabels(cr)
	lbls["app.kubernetes.io/component"] = "mongos"
	return lbls
}

func GetRSPods(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, rsName string, includeOutdated bool) (corev1.PodList, error) {
	pods := corev1.PodList{}
	err := k8sclient.List(ctx,
		&pods,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(rsLabels(cr, rsName)),
		},
	)
	if err != nil {
		return pods, errors.Wrap(err, "failed to list pods")
	}

	if includeOutdated {
		return pods, nil
	}

	sort.Slice(pods.Items, func(i, j int) bool {
		return pods.Items[i].Name < pods.Items[j].Name
	})

	rs := cr.Spec.Replset(rsName)
	if rs == nil {
		return pods, errors.Errorf("replset %s is not found", rsName)
	}

	var rsPods []corev1.Pod
	if rs.ClusterRole == api.ClusterRoleConfigSvr {
		rsPods = filterPodsByComponent(pods, api.ConfigReplSetName)
	} else {
		rsPods = filterPodsByComponent(pods, "mongod")
	}

	arbiterPods := filterPodsByComponent(pods, "arbiter")
	nvPods := filterPodsByComponent(pods, "nonVoting")

	if len(rsPods) >= int(rs.Size) {
		rsPods = rsPods[:rs.Size]
	}
	if len(arbiterPods) >= int(rs.Arbiter.Size) {
		arbiterPods = arbiterPods[:rs.Arbiter.Size]
	}
	if len(nvPods) >= int(rs.NonVoting.Size) {
		nvPods = nvPods[:rs.NonVoting.Size]
	}
	pods.Items = rsPods
	pods.Items = append(pods.Items, arbiterPods...)
	pods.Items = append(pods.Items, nvPods...)

	return pods, nil
}

func filterPodsByComponent(list corev1.PodList, component string) []corev1.Pod {
	var pods []corev1.Pod
	for _, pod := range list.Items {
		v, ok := pod.Labels["app.kubernetes.io/component"]
		if !ok {
			continue
		}
		if v == component {
			pods = append(pods, pod)
		}
	}
	return pods
}

func GetPrimaryPod(ctx context.Context, client *mgo.Client) (string, error) {
	status, err := mongo.RSStatus(ctx, client)
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
			LabelSelector: labels.SelectorFromSet(mongosLabels(cr)),
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
			LabelSelector: labels.SelectorFromSet(mongosLabels(cr)),
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
