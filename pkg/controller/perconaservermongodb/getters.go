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
			Namespace: cr.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"app.kubernetes.io/name":       "percona-server-mongodb",
				"app.kubernetes.io/instance":   cr.Name,
				"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
				"app.kubernetes.io/part-of":    "percona-server-mongodb",
				"app.kubernetes.io/component":  "mongos",
			}),
		},
	)

	return mongosPods, err
}

func (r *ReconcilePerconaServerMongoDB) getRSPods(cr *api.PerconaServerMongoDB, rsName string) (corev1.PodList, error) {
	matchLabels := map[string]string{
		"app.kubernetes.io/name":       "percona-server-mongodb",
		"app.kubernetes.io/instance":   cr.Name,
		"app.kubernetes.io/replset":    rsName,
		"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
		"app.kubernetes.io/part-of":    "percona-server-mongodb",
	}

	pods := corev1.PodList{}
	err := r.client.List(context.TODO(),
		&pods,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(matchLabels),
		},
	)

	return pods, err
}
