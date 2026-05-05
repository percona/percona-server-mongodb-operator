package perconaservermongodb

import (
	"context"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	psmdbconfig "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/logcollector"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/logcollector/logrotate"
)

func (r *ReconcilePerconaServerMongoDB) reconcileStatefulSet(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, ls map[string]string) (*appsv1.StatefulSet, error) {
	log := logf.FromContext(ctx)

	pdbspec := rs.PodDisruptionBudget
	volumeSpec := rs.VolumeSpec

	if rs.ClusterRole == api.ClusterRoleConfigSvr {
		ls[naming.LabelKubernetesComponent] = api.ConfigReplSetName
	}

	switch ls[naming.LabelKubernetesComponent] {
	case naming.ComponentArbiter:
		pdbspec = rs.Arbiter.PodDisruptionBudget
	case naming.ComponentNonVoting:
		pdbspec = rs.NonVoting.PodDisruptionBudget
		volumeSpec = rs.NonVoting.VolumeSpec
	case naming.ComponentHidden:
		pdbspec = rs.Hidden.PodDisruptionBudget
		volumeSpec = rs.Hidden.VolumeSpec
	}

	sfs, err := r.getStatefulsetFromReplset(ctx, cr, rs, ls)
	if err != nil {
		return nil, errors.Wrapf(err, "get StatefulSet for replset %s", rs.Name)
	}

	_, ok := sfs.Annotations[api.AnnotationRestoreInProgress]
	if ok {
		if err := r.smartUpdate(ctx, cr, sfs, rs); err != nil {
			return nil, errors.Wrap(err, "failed to run smartUpdate")
		}

		log.V(1).Info("Restore in progress, skipping reconciliation of statefulset", "name", sfs.Name)
		return sfs, nil
	}

	if err := r.reconcileStorageAutoscaling(ctx, cr, sfs, volumeSpec, ls); err != nil {
		log.Error(err, "failed to reconcile storage autoscaling", "statefulset", sfs.Name)
	}

	if err := r.reconcilePVCs(ctx, cr, sfs, ls, volumeSpec); err != nil {
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

	err = r.reconcilePDB(ctx, cr, pdbspec, ls, cr.Namespace, sfs)
	if err != nil {
		return nil, errors.Wrapf(err, "PodDisruptionBudget for %s", sfs.Name)
	}

	if err := r.smartUpdate(ctx, cr, sfs, rs); err != nil {
		return nil, errors.Wrap(err, "failed to run smartUpdate")
	}

	return sfs, nil
}

func (r *ReconcilePerconaServerMongoDB) getStatefulsetFromReplset(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, ls map[string]string) (*appsv1.StatefulSet, error) {
	sfsName := cr.Name + "-" + rs.Name
	mongodCustomConfigName := naming.MongodCustomConfigName(cr, rs)

	if rs.ClusterRole == api.ClusterRoleConfigSvr {
		ls[naming.LabelKubernetesComponent] = api.ConfigReplSetName
	}

	switch ls[naming.LabelKubernetesComponent] {
	case naming.ComponentArbiter:
		sfsName += "-" + naming.ComponentArbiter
	case naming.ComponentNonVoting:
		sfsName += "-" + naming.ComponentNonVotingShort
		mongodCustomConfigName = naming.NonVotingConfigMapName(cr, rs)
	case naming.ComponentHidden:
		sfsName += "-" + naming.ComponentHidden
		mongodCustomConfigName = naming.HiddenConfigMapName(cr, rs)
	}

	sfs := psmdb.NewStatefulSet(sfsName, cr.Namespace)
	err := setControllerReference(cr, sfs, r.scheme)
	if err != nil {
		return nil, errors.Wrapf(err, "set owner ref for StatefulSet %s", sfs.Name)
	}

	err = r.client.Get(ctx, types.NamespacedName{Name: sfs.Name, Namespace: sfs.Namespace}, sfs)
	if client.IgnoreNotFound(err) != nil {
		return nil, errors.Wrapf(err, "get StatefulSet %s", sfs.Name)
	}

	mongodCustomConfig, err := r.getCustomConfig(ctx, cr.Namespace, mongodCustomConfigName)
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

	configs := psmdb.StatefulConfigParams{
		MongoDConf:         mongodCustomConfig,
		LogCollectionConf:  logCollectionCustomConfig,
		LogRotateConf:      logRotateCustomConfig,
		LogRotateExtraConf: logRotateExtraConfig,
	}
	secrets := psmdb.StatefulSpecSecretParams{
		UsersSecret: usersSecret,
		SSLSecret:   sslSecret,
	}
	sfsSpec, err := psmdb.StatefulSpec(
		ctx, cr, rs, ls, r.initImage,
		configs,
		secrets,
	)
	if err != nil {
		return nil, errors.Wrapf(err, "create StatefulSet.Spec %s", sfs.Name)
	}

	sfs.Labels = sfsSpec.Template.Labels
	sfs.Spec = sfsSpec

	sslAnn, err := r.sslAnnotation(ctx, cr)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get ssl annotations")
	}
	for k, v := range sslAnn {
		sfsSpec.Template.Annotations[k] = v
	}

	if preservedValue, exists := cr.Annotations[api.AnnotationPreservedRestartedAt(sfs.Name)]; exists {
		sfsSpec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = preservedValue
	}

	return sfs, nil
}
