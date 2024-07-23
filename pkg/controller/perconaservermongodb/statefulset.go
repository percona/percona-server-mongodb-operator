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
)

func (r *ReconcilePerconaServerMongoDB) reconcileStatefulSet(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, ls map[string]string) (*appsv1.StatefulSet, error) {
	log := logf.FromContext(ctx)

	pdbspec := rs.PodDisruptionBudget
	volumeSpec := rs.VolumeSpec

	if rs.ClusterRole == api.ClusterRoleConfigSvr {
		ls[naming.LabelKubernetesComponent] = api.ConfigReplSetName
	}

	switch ls[naming.LabelKubernetesComponent] {
	case "arbiter":
		pdbspec = rs.Arbiter.PodDisruptionBudget
	case "nonVoting":
		pdbspec = rs.NonVoting.PodDisruptionBudget
		volumeSpec = rs.NonVoting.VolumeSpec
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

	if err := r.reconcilePVCs(ctx, cr, sfs, ls, volumeSpec.PersistentVolumeClaim); err != nil {
		return nil, errors.Wrapf(err, "reconcile PVCs for %s", sfs.Name)
	}

	if _, ok := sfs.Annotations[api.AnnotationPVCResizeInProgress]; ok {
		log.V(1).Info("PVC resize in progress, skipping reconciliation of statefulset", "name", sfs.Name)
		return sfs, nil
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
	configName := psmdb.MongodCustomConfigName(cr.Name, rs.Name)

	if rs.ClusterRole == api.ClusterRoleConfigSvr {
		ls[naming.LabelKubernetesComponent] = api.ConfigReplSetName
	}

	switch ls[naming.LabelKubernetesComponent] {
	case "arbiter":
		sfsName += "-arbiter"
	case "nonVoting":
		sfsName += "-nv"
		configName = psmdb.MongodCustomConfigName(cr.Name, rs.Name+"-nv")
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

	customConfig, err := r.getCustomConfig(ctx, cr.Namespace, configName)
	if err != nil {
		return nil, errors.Wrap(err, "check if mongod custom configuration exists")
	}

	secret := new(corev1.Secret)
	err = r.client.Get(ctx, types.NamespacedName{Name: api.UserSecretName(cr), Namespace: cr.Namespace}, secret)
	if client.IgnoreNotFound(err) != nil {
		return nil, errors.Wrap(err, "check pmm secrets")
	}

	sfsSpec, err := psmdb.StatefulSpec(ctx, cr, rs, ls, r.initImage, customConfig, secret)
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

	return sfs, nil
}
