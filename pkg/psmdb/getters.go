package psmdb

import (
	"context"

	mgo "go.mongodb.org/mongo-driver/mongo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
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

func GetRSPods(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, rsName string) (corev1.PodList, error) {
	pods := corev1.PodList{}
	err := k8sclient.List(ctx,
		&pods,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(rsLabels(cr, rsName)),
		},
	)

	return pods, err
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
