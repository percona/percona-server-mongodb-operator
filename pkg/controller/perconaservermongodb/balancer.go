package perconaservermongodb

import (
	"context"
	"time"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func (r *ReconcilePerconaServerMongoDB) enableBalancerIfNeeded(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	if s := cr.Spec.Sharding; !s.Enabled ||
		s.Mongos.Size == 0 ||
		!s.Balancer.IsEnabled() ||
		cr.Spec.Unmanaged ||
		cr.DeletionTimestamp != nil ||
		cr.Spec.Pause {

		return nil
	}

	uptodate, err := r.isAllSfsUpToDate(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to check if all sfs are up to date")
	}

	bcpRunning, err := r.isBackupRunning(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to check running backups")
	}

	rstRunning, err := r.isRestoreRunning(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to check running restores")
	}

	if !uptodate || bcpRunning || rstRunning {
		return nil
	}

	msSts := psmdb.MongosStatefulset(cr)

	for {
		err = r.client.Get(ctx, types.NamespacedName{Name: msSts.Name, Namespace: msSts.Namespace}, msSts)
		if err != nil && !k8sErrors.IsNotFound(err) {
			return errors.Wrapf(err, "get statefulset %s", msSts.Name)
		}

		if msSts.ObjectMeta.Generation == msSts.Status.ObservedGeneration {
			break
		}

		time.Sleep(1 * time.Second)
	}

	if msSts.Status.UpdatedReplicas < msSts.Status.Replicas {
		log.Info("waiting for mongos update", "updated", msSts.Status.UpdatedReplicas, "replicas", msSts.Status.Replicas)
		return nil
	}

	mongosPods, err := r.getMongosPods(ctx, cr)
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

	mongosSession, err := r.mongosClientWithRole(ctx, cr, api.RoleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "failed to get mongos connection")
	}

	defer func() {
		err := mongosSession.Disconnect(ctx)
		if err != nil {
			log.Error(err, "failed to close mongos connection")
		}
	}()

	run, err := mongosSession.IsBalancerRunning(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to check if balancer running")
	}

	if !run {
		err := mongosSession.StartBalancer(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to start balancer")
		}

		log.Info("balancer enabled")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) disableBalancerIfNeeded(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if s := cr.Spec.Sharding; !s.Enabled ||
		s.Mongos.Size == 0 ||
		s.Balancer.IsEnabled() ||
		cr.Spec.Unmanaged {
		return nil
	}
	return r.disableBalancer(ctx, cr)
}

func (r *ReconcilePerconaServerMongoDB) disableBalancer(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	if s := cr.Spec.Sharding; !s.Enabled || s.Mongos.Size == 0 || cr.Spec.Unmanaged {
		return nil
	}

	msSts := psmdb.MongosStatefulset(cr)

	err := r.client.Get(ctx, cr.MongosNamespacedName(), msSts)
	if k8sErrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return errors.Wrapf(err, "get mongos statefulset %s", msSts.Name)
	}

	mongosSession, err := r.mongosClientWithRole(ctx, cr, api.RoleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "failed to get mongos connection")
	}

	defer func() {
		err := mongosSession.Disconnect(ctx)
		if err != nil {
			log.Error(err, "failed to close mongos connection")
		}
	}()

	run, err := mongosSession.IsBalancerRunning(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to check if balancer running")
	}

	if run {
		err := mongosSession.StopBalancer(ctx)
		if err != nil {
			return errors.Wrap(err, "failed to stop balancer")
		}

		log.Info("balancer disabled")
	}

	return nil
}
