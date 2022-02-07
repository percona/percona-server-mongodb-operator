package perconaservermongodb

import (
	"context"
	"time"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

func (r *ReconcilePerconaServerMongoDB) enableBalancerIfNeeded(cr *api.PerconaServerMongoDB) error {
	if !cr.Spec.Sharding.Enabled || cr.Spec.Sharding.Mongos.Size == 0 || cr.Spec.Unmanaged {
		return nil
	}

	uptodate, err := r.isAllSfsUpToDate(cr)
	if err != nil {
		return errors.Wrap(err, "failed to check if all sfs are up to date")
	}

	rstRunning, err := r.isRestoreRunning(cr)
	if err != nil {
		return errors.Wrap(err, "failed to check running restores")
	}

	if !uptodate || rstRunning {
		return nil
	}

	msSts := psmdb.MongosStatefulset(cr)

	for {
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: msSts.Name, Namespace: msSts.Namespace}, msSts)
		if err != nil && !k8sErrors.IsNotFound(err) {
			return errors.Wrapf(err, "get statefulset %s", msSts.Name)
		}

		if msSts.ObjectMeta.Generation == msSts.Status.ObservedGeneration {
			break
		}

		time.Sleep(1 * time.Second)
	}

	if msSts.Status.UpdatedReplicas < msSts.Status.Replicas {
		log.Info("waiting for mongos update")
		return nil
	}

	mongosPods, err := r.getMongosPods(cr)
	if err != nil && !k8sErrors.IsNotFound(err) {
		return errors.Wrap(err, "get pods list for mongos")
	}

	if len(mongosPods.Items) == 0 {
		return nil
	}
	for _, p := range mongosPods.Items {
		if p.Status.Phase != corev1.PodRunning {
			return nil
		}
		for _, cs := range p.Status.ContainerStatuses {
			if !cs.Ready {
				return nil
			}
		}
	}

	mongosSession, err := r.mongosClientWithRole(cr, roleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "failed to get mongos connection")
	}

	defer func() {
		err := mongosSession.Disconnect(context.TODO())
		if err != nil {
			log.Error(err, "failed to close mongos connection")
		}
	}()

	run, err := mongo.IsBalancerRunning(context.TODO(), mongosSession)
	if err != nil {
		return errors.Wrap(err, "failed to check if balancer running")
	}

	if !run {
		err := mongo.StartBalancer(context.TODO(), mongosSession)
		if err != nil {
			return errors.Wrap(err, "failed to start balancer")
		}

		log.Info("balancer enabled")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) disableBalancer(cr *api.PerconaServerMongoDB) error {
	if !cr.Spec.Sharding.Enabled || cr.Spec.Unmanaged {
		return nil
	}

	msSts := psmdb.MongosStatefulset(cr)

	err := r.client.Get(context.TODO(), cr.MongosNamespacedName(), msSts)
	if k8sErrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return errors.Wrapf(err, "get mongos statefulset %s", msSts.Name)
	}

	mongosSession, err := r.mongosClientWithRole(cr, roleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "failed to get mongos connection")
	}

	defer func() {
		err := mongosSession.Disconnect(context.TODO())
		if err != nil {
			log.Error(err, "failed to close mongos connection")
		}
	}()

	run, err := mongo.IsBalancerRunning(context.TODO(), mongosSession)
	if err != nil {
		return errors.Wrap(err, "failed to check if balancer running")
	}

	if run {
		err := mongo.StopBalancer(context.TODO(), mongosSession)
		if err != nil {
			return errors.Wrap(err, "failed to stop balancer")
		}

		log.Info("balancer disabled")
	}

	return nil
}
