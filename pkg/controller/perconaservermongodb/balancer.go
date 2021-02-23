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
	if !cr.Spec.Sharding.Enabled || cr.Spec.Sharding.Mongos.Size == 0 {
		return nil
	}

	uptodate, err := r.isAllSfsUpToDate(cr)
	if err != nil {
		return errors.Wrap(err, "failed to chaeck if all sfs are up to date")
	}

	rstRunning, err := r.isRestoreRunning(cr)
	if err != nil {
		return errors.Wrap(err, "failed to check running restores")
	}

	if !uptodate || rstRunning {
		return nil
	}

	msDepl := psmdb.MongosDeployment(cr)

	for {
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: msDepl.Name, Namespace: msDepl.Namespace}, msDepl)
		if err != nil && !k8sErrors.IsNotFound(err) {
			return errors.Wrapf(err, "get deployment %s", msDepl.Name)
		}

		if msDepl.ObjectMeta.Generation == msDepl.Status.ObservedGeneration {
			break
		}

		time.Sleep(1 * time.Second)
	}

	if msDepl.Status.UpdatedReplicas < msDepl.Status.Replicas {
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
	if !cr.Spec.Sharding.Enabled {
		return nil
	}

	msDepl := psmdb.MongosDeployment(cr)

	err := r.client.Get(context.TODO(), cr.MongosNamespacedName(), msDepl)
	if k8sErrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return errors.Wrapf(err, "get mongos deployment %s", msDepl.Name)
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
