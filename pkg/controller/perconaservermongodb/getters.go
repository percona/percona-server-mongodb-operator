package perconaservermongodb

import (
	"context"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func (r *ReconcilePerconaServerMongoDB) getMongodPods(ctx context.Context, cr *api.PerconaServerMongoDB) (corev1.PodList, error) {
	mongodPods := corev1.PodList{}
	err := r.client.List(ctx,
		&mongodPods,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(api.MongodLabels(cr)),
		},
	)

	return mongodPods, err
}


func (r *ReconcilePerconaServerMongoDB) getMongosPods(ctx context.Context, cr *api.PerconaServerMongoDB) (corev1.PodList, error) {
	mongosPods := corev1.PodList{}
	err := r.client.List(ctx,
		&mongosPods,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(api.MongosLabels(cr)),
		},
	)

	return mongosPods, err
}

func (r *ReconcilePerconaServerMongoDB) getArbiterStatefulset(ctx context.Context, cr *api.PerconaServerMongoDB, rs string) (appsv1.StatefulSet, error) {
	list := appsv1.StatefulSetList{}

	l := api.ArbiterLabels(cr)
	l["app.kubernetes.io/replset"] = rs

	err := r.client.List(ctx,
		&list,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(l),
		},
	)

	if len(list.Items) != 1 {
		return appsv1.StatefulSet{}, errors.Errorf("invalid sfs arbiter count: %d", len(list.Items))
	}

	return list.Items[0], err
}

func (r *ReconcilePerconaServerMongoDB) getRsStatefulset(ctx context.Context, cr *api.PerconaServerMongoDB, rs string) (appsv1.StatefulSet, error) {
	sts := appsv1.StatefulSet{}

	err := r.client.Get(ctx, cr.StatefulsetNamespacedName(rs), &sts)

	return sts, err
}

func (r *ReconcilePerconaServerMongoDB) getArbiterStatefulsets(ctx context.Context, cr *api.PerconaServerMongoDB) (appsv1.StatefulSetList, error) {
	list := appsv1.StatefulSetList{}

	err := r.client.List(ctx,
		&list,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(api.ArbiterLabels(cr)),
		},
	)

	return list, err
}

func (r *ReconcilePerconaServerMongoDB) getMongodStatefulsets(ctx context.Context, cr *api.PerconaServerMongoDB) (appsv1.StatefulSetList, error) {
	list := appsv1.StatefulSetList{}

	err := r.client.List(ctx,
		&list,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(api.MongodLabels(cr)),
		},
	)

	return list, err
}

func (r *ReconcilePerconaServerMongoDB) getStatefulsetsExceptMongos(ctx context.Context, cr *api.PerconaServerMongoDB) (appsv1.StatefulSetList, error) {
	list := appsv1.StatefulSetList{}

	selectors := labels.SelectorFromSet(api.ClusterLabels(cr))

	req, err := labels.NewRequirement("app.kubernetes.io/component", selection.NotEquals, []string{"mongos"})
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
			LabelSelector: labels.SelectorFromSet(api.ClusterLabels(cr)),
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
			LabelSelector: labels.SelectorFromSet(api.ClusterLabels(cr)),
		},
	)

	return list, err
}

func (r *ReconcilePerconaServerMongoDB) getMongodPVCs(ctx context.Context, cr *api.PerconaServerMongoDB) (corev1.PersistentVolumeClaimList, error) {
	list := corev1.PersistentVolumeClaimList{}

	err := r.client.List(ctx,
		&list,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(api.MongodLabels(cr)),
		},
	)

	return list, err
}
