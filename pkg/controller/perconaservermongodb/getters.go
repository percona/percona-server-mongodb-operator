package perconaservermongodb

import (
	"context"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *ReconcilePerconaServerMongoDB) getMongosPods(cr *api.PerconaServerMongoDB) (corev1.PodList, error) {
	mongosPods := corev1.PodList{}
	err := r.client.List(context.TODO(),
		&mongosPods,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(mongosLabels(cr)),
		},
	)

	return mongosPods, err
}

func (r *ReconcilePerconaServerMongoDB) getRSPods(cr *api.PerconaServerMongoDB, rsName string) (corev1.PodList, error) {
	pods := corev1.PodList{}
	err := r.client.List(context.TODO(),
		&pods,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(rsLabels(cr, rsName)),
		},
	)

	return pods, err
}

func (r *ReconcilePerconaServerMongoDB) getAllPVCs(cr *api.PerconaServerMongoDB) (corev1.PersistentVolumeClaimList, error) {
	list := corev1.PersistentVolumeClaimList{}

	err := r.client.List(context.TODO(),
		&list,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(clusterLabels(cr)),
		},
	)

	return list, err
}

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
