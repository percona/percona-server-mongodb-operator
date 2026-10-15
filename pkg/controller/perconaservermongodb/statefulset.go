package perconaservermongodb

import (
	"context"
	"fmt"
	"maps"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	psmdbconfig "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/logcollector"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/logcollector/logrotate"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
)

func (r *ReconcilePerconaServerMongoDB) reconcileStatefulSet(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	rs *api.ReplsetSpec,
	group membergroup.Group,
) (*appsv1.StatefulSet, error) {
	log := logf.FromContext(ctx)

	sfs, err := r.getStatefulsetFromReplset(ctx, cr, rs, group)
	if err != nil {
		return nil, errors.Wrapf(err, "get StatefulSet %s for replset %s", group.STSName, rs.Name)
	}

	_, ok := sfs.Annotations[api.AnnotationRestoreInProgress]
	if ok {
		if err := r.smartUpdate(ctx, cr, sfs, rs, group); err != nil {
			return nil, errors.Wrap(err, "failed to run smartUpdate")
		}

		log.V(1).Info("Restore in progress, skipping reconciliation of statefulset", "name", sfs.Name)
		return sfs, nil
	}

	if err := r.reconcileGroupStorageAutoscaling(ctx, cr, sfs, group); err != nil {
		log.Error(err, "failed to reconcile storage autoscaling", "statefulset", sfs.Name)
	}

	if err := r.reconcilePVCs(ctx, cr, sfs, group); err != nil {
		return nil, errors.Wrapf(err, "reconcile PVCs for %s", sfs.Name)
	}

	// Re-read the StatefulSet from the cluster to check for the PVC resize
	// annotation, as it may have been set during reconcilePVCs and the local
	// sfs object would be stale.
	currentSts := new(appsv1.StatefulSet)
	if err := r.client.Get(ctx, types.NamespacedName{Name: sfs.Name, Namespace: sfs.Namespace}, currentSts); err == nil {
		if _, ok := currentSts.Annotations[api.AnnotationPVCResizeInProgress]; ok {
			log.V(1).Info("PVC resize in progress, skipping reconciliation of statefulset", "name", sfs.Name)
			return sfs, nil
		}
	}

	err = r.createOrUpdate(ctx, sfs)
	if err != nil {
		return nil, errors.Wrapf(err, "update StatefulSet %s", sfs.Name)
	}

	if err := r.smartUpdate(ctx, cr, sfs, rs, group); err != nil {
		return nil, errors.Wrap(err, "failed to run smartUpdate")
	}

	return sfs, nil
}

// cleanupStalePDBs deletes any per-component PDBs that the operator used to create (this was a bug).
// The operator instead creates per-replset PDBs, however we need the old ones deleted first.
func (r *ReconcilePerconaServerMongoDB) cleanupStalePDBs(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	rs *api.ReplsetSpec,
) error {
	toDelete := []string{
		fmt.Sprintf("%s-%s-%s", cr.Name, naming.ComponentArbiter, rs.Name),
		fmt.Sprintf("%s-%s-%s", cr.Name, naming.ComponentHidden, rs.Name),
		fmt.Sprintf("%s-%s-%s", cr.Name, naming.ComponentMongod, rs.Name),
		fmt.Sprintf("%s-%s-%s", cr.Name, naming.ComponentNonVoting, rs.Name),
		fmt.Sprintf("%s-%s-%s", cr.Name, naming.ComponentConfigSrv, naming.ComponentConfigSrv),
		fmt.Sprintf("%s-%s-", cr.Name, naming.ComponentMongos),
	}

	for _, pdbName := range toDelete {
		pdb := &policyv1.PodDisruptionBudget{
			Name:      pdbName,
			Namespace: cr.Namespace,
		}

		if err := k8s.DeleteIfExists(ctx, r.client, pdb); err != nil {
			return errors.Wrapf(err, "delete stale PDB %s", pdbName)
		}
	}
	return nil

}

func (r *ReconcilePerconaServerMongoDB) getStatefulsetFromReplset(
	ctx context.Context,
	cr *api.PerconaServerMongoDB,
	rs *api.ReplsetSpec,
	group membergroup.Group,
) (*appsv1.StatefulSet, error) {
	sfs := psmdb.NewStatefulSet(group.STSName, cr.Namespace)
	if err := setControllerReference(cr, sfs, r.scheme); err != nil {
		return nil, errors.Wrapf(err, "set owner ref for StatefulSet %s", sfs.Name)
	}

	if err := r.client.Get(ctx, client.ObjectKeyFromObject(sfs), sfs); client.IgnoreNotFound(err) != nil {
		return nil, errors.Wrapf(err, "get StatefulSet %s", sfs.Name)
	}

	mongodCustomConfig, err := r.getCustomConfig(ctx, cr.Namespace, group.ConfigName)
	if err != nil {
		return nil, errors.Wrap(err, "check if mongod custom configuration exists")
	}

	logCollectionCustomConfig, err := r.getCustomConfig(ctx, cr.Namespace, logcollector.ConfigMapName(cr.Name))
	if err != nil {
		return nil, errors.Wrap(err, "check if log collection custom configuration exists")
	}

	var logRotateCustomConfig, logRotateExtraConfig psmdbconfig.CustomConfig
	if cr.CompareVersion("1.22.0") >= 0 {
		logRotateCustomConfig, err = r.getCustomConfig(ctx, cr.Namespace, logrotate.ConfigMapName(cr.Name))
		if err != nil {
			return nil, errors.Wrap(err, "check if log rotate configuration exists")
		}

		if cr.Spec.LogCollector != nil && cr.Spec.LogCollector.LogRotate != nil && cr.Spec.LogCollector.LogRotate.ExtraConfig.Name != "" {
			logRotateExtraConfig, err = r.getCustomConfig(ctx, cr.Namespace, cr.Spec.LogCollector.LogRotate.ExtraConfig.Name)
			if err != nil {
				return nil, errors.Wrap(err, "check if log rotate extra configuration exists")
			}
		}
	}

	usersSecret := new(corev1.Secret)
	err = r.client.Get(ctx, types.NamespacedName{Name: api.UserSecretName(cr), Namespace: cr.Namespace}, usersSecret)
	if client.IgnoreNotFound(err) != nil {
		return nil, errors.Wrap(err, "check pmm secrets")
	}

	sslSecret := new(corev1.Secret)
	err = r.client.Get(ctx, types.NamespacedName{Name: api.SSLSecretName(cr), Namespace: cr.Namespace}, sslSecret)
	if client.IgnoreNotFound(err) != nil {
		return nil, errors.Wrap(err, "check ssl secrets")
	}

	keyfileSecretErr := r.client.Get(ctx, types.NamespacedName{Name: cr.Spec.Secrets.GetInternalKey(cr), Namespace: cr.Namespace}, &corev1.Secret{})
	if client.IgnoreNotFound(keyfileSecretErr) != nil {
		return nil, errors.Wrap(keyfileSecretErr, "check keyfile secret")
	}

	configs := psmdb.StatefulConfigParams{
		MongoDConf:         mongodCustomConfig,
		LogCollectionConf:  logCollectionCustomConfig,
		LogRotateConf:      logRotateCustomConfig,
		LogRotateExtraConf: logRotateExtraConfig,
	}
	secrets := psmdb.StatefulSpecSecretParams{
		UsersSecret:   usersSecret,
		SSLSecret:     sslSecret,
		KeyfileExists: keyfileSecretErr == nil,
	}

	sfsSpec, err := psmdb.StatefulSpec(ctx, cr, rs, group, r.initImage, configs, secrets)
	if err != nil {
		return nil, errors.Wrapf(err, "create StatefulSet.Spec %s", sfs.Name)
	}

	sfs.Labels = sfsSpec.Template.Labels
	sfs.Spec = sfsSpec

	sslAnn, err := r.sslAnnotation(ctx, cr)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get ssl annotations")
	}
	maps.Copy(sfsSpec.Template.Annotations, sslAnn)

	if preservedValue, exists := cr.Annotations[api.AnnotationPreservedRestartedAt(sfs.Name)]; exists {
		sfsSpec.Template.Annotations[naming.AnnotationKubectlRestartedAt] = preservedValue
	}

	return sfs, nil
}
