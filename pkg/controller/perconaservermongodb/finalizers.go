package perconaservermongodb

import (
	"context"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	psmdb_mongo "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/mongo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sort"
)

func (r *ReconcilePerconaServerMongoDB) checkFinalizers(cr *api.PerconaServerMongoDB) error {
	finalizersRemain := []string{}

	finalizers := cr.GetFinalizers()
	// delete-psmdb-in-order should be executed first
	sort.Slice(finalizers, func(i, j int) bool {
		if finalizers[i] == "delete-psmdb-in-order" {
			return true
		}
		return finalizers[i] < finalizers[j]
	})

	for _, f := range finalizers {
		var err error
		switch f {
		case "delete-psmdb-pvc":
			err = r.deletePvcFinalizer(cr)
		case "delete-psmdb-in-order":
			err = r.deletePSMDBPods(cr)
			if err != nil {
				return err
			}
		}
		if err != nil {
			log.Error(err, "failed to run finalizer", "finalizer", f)
			finalizersRemain = append(finalizersRemain, f)
		}
	}

	cr.SetFinalizers(finalizersRemain)
	err := r.client.Update(context.TODO(), cr)

	return err
}

func (r *ReconcilePerconaServerMongoDB) deletePSMDBPods(cr *api.PerconaServerMongoDB) error {
	stsList, err := r.getAllstatefulsets(cr)
	if err != nil {
		return errors.Wrap(err, "failed to get StatefulSet list")
	}

	for _, sts := range stsList.Items {
		if err = r.deleteStatefulSetPods(cr, sts); err != nil {
			return errors.Wrap(err, "failed to delete StatefulSet pods")
		}
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteStatefulSetPods(cr *api.PerconaServerMongoDB, sts appsv1.StatefulSet) error {
	list := corev1.PodList{}

	err := r.client.List(context.TODO(),
		&list,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(sts.Labels),
		},
	)
	if err != nil {
		return errors.Wrap(err, "get pod list")
	}

	// the last pod left - we can leave it for the stateful set
	if len(list.Items) <= 1 {
		return nil
	}

	err = r.client.Get(context.TODO(), types.NamespacedName{Name: sts.Name, Namespace: sts.Namespace}, &sts)
	if err != nil {
		return errors.Wrap(err, "get StatefulSet")
	}
	var replset *api.ReplsetSpec
	for _, rs := range cr.Spec.Replsets {
		if rs.Name == sts.Labels["app.kubernetes.io/replset"] {
			replset = rs
			break
		}
	}
	var cli *mongo.Client
	if cr.Spec.Sharding.Enabled {
		cli, err = r.mongosClientWithRole(cr, roleClusterAdmin)
	} else {
		cli, err = r.mongoClientWithRole(cr, *replset, roleClusterAdmin)
	}
	if err != nil {
		return errors.Wrap(err, "mongo client")
	}
	err = r.stepDownUntilFirstIsPrimary(context.TODO(), cli)
	if err != nil {
		return errors.Wrap(err, "step down until first is primary")
	}
	cnf, err := psmdb_mongo.ReadConfig(context.TODO(), cli)
	if err != nil {
		return errors.Wrap(err, "get mongo config")
	}
	if len(cnf.Members) > 1 {
		sort.Slice(cnf.Members, func(i, j int) bool {
			return cnf.Members[i].Host < cnf.Members[j].Host
		})
		cnf.Members = cnf.Members[:len(cnf.Members)-1]
		cnf.Version++
		err = psmdb_mongo.WriteConfig(context.TODO(), cli, cnf)
		if err != nil {
			return errors.Wrap(err, "delete: write mongo config")
		}
	}

	if *sts.Spec.Replicas != 1 {
		dscaleTo := *sts.Spec.Replicas - 1
		sts.Spec.Replicas = &dscaleTo
		err = r.client.Update(context.TODO(), &sts)
		if err != nil {
			return errors.Wrap(err, "downscale StatefulSet")
		}
	}
	return errors.New("waiting pods to be deleted")
}

func (r *ReconcilePerconaServerMongoDB) stepDownUntilFirstIsPrimary(ctx context.Context, cli *mongo.Client) error {
	status, err := psmdb_mongo.RSStatus(context.TODO(), cli)
	if err != nil {
		return errors.Wrap(err, "rs status")
	}
	sort.Slice(status.Members, func(i, j int) bool {
		return status.Members[i].Name < status.Members[j].Name
	})
	if status.Members[0].State != psmdb_mongo.MemberStatePrimary {
		if err := psmdb_mongo.StepDown(ctx, cli, true); err != nil {
			return errors.Wrap(err, "step down")
		}
		if err := r.stepDownUntilFirstIsPrimary(ctx, cli); err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) deletePvcFinalizer(cr *api.PerconaServerMongoDB) error {
	err := r.deleteAllStatefulsets(cr)
	if err != nil {
		return errors.Wrap(err, "failed to delete all StatefulSets")
	}

	err = r.deleteAllPVC(cr)
	if err != nil {
		return errors.Wrap(err, "failed to delete all PVCs")
	}

	err = r.deleteSecrets(cr)
	if err != nil {
		return errors.Wrap(err, "failed to delete secrets")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteAllStatefulsets(cr *api.PerconaServerMongoDB) error {
	stsList, err := r.getAllstatefulsets(cr)
	if err != nil {
		return errors.Wrap(err, "failed to get StatefulSet list")
	}

	for _, sts := range stsList.Items {
		log.Info("deleting StatefulSet", "name", sts.Name)
		err := r.client.Delete(context.TODO(), &sts)
		if err != nil {
			return errors.Wrapf(err, "failed to delete StatefulSet %s", sts.Name)
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteAllPVC(cr *api.PerconaServerMongoDB) error {
	pvcList, err := r.getAllPVCs(cr)
	if err != nil {
		return errors.Wrap(err, "failed to get PVC list")
	}

	for _, pvc := range pvcList.Items {
		log.Info("deleting PVC", "name", pvc.Name)
		err := r.client.Delete(context.TODO(), &pvc)
		if err != nil {
			return errors.Wrapf(err, "failed to delete PVC %s", pvc.Name)
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteSecrets(cr *api.PerconaServerMongoDB) error {
	secrets := []string{
		cr.Spec.Secrets.Users,
		"internal-" + cr.Name,
		"internal-" + cr.Name + "-users",
		cr.Name + "-mongodb-encryption-key",
	}

	for _, secretName := range secrets {
		secret := &corev1.Secret{}
		err := r.client.Get(context.TODO(), types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      secretName,
		}, secret)

		if err != nil && !k8serrors.IsNotFound(err) {
			return errors.Wrap(err, "get secret")
		}

		if k8serrors.IsNotFound(err) {
			continue
		}

		log.Info("deleting secret", "name", secret.Name)
		err = r.client.Delete(context.TODO(), secret)
		if err != nil {
			return errors.Wrapf(err, "delete secret %s", secretName)
		}
	}

	return nil
}
