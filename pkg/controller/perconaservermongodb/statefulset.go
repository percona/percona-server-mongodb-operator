package perconaservermongodb

import (
	"context"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// TODO: reduce cyclomatic complexity
func (r *ReconcilePerconaServerMongoDB) reconcileStatefulSet(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, ls map[string]string) (*appsv1.StatefulSet, error) {
	log := logf.FromContext(ctx)

	pdbspec := rs.PodDisruptionBudget
	volumeSpec := rs.VolumeSpec

	if rs.ClusterRole == api.ClusterRoleConfigSvr {
		ls["app.kubernetes.io/component"] = api.ConfigReplSetName
	}

	switch ls["app.kubernetes.io/component"] {
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

	err = r.createOrUpdate(ctx, sfs)
	if err != nil {
		return nil, errors.Wrapf(err, "update StatefulSet %s", sfs.Name)
	}

	err = r.reconcilePDB(ctx, pdbspec, ls, cr.Namespace, sfs)
	if err != nil {
		return nil, errors.Wrapf(err, "PodDisruptionBudget for %s", sfs.Name)
	}

	if err := r.reconcilePVCs(ctx, sfs, ls, volumeSpec.PersistentVolumeClaim); err != nil {
		return nil, errors.Wrapf(err, "reconcile PVCs for %s", sfs.Name)
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
		ls["app.kubernetes.io/component"] = api.ConfigReplSetName
	}

	switch ls["app.kubernetes.io/component"] {
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

	if cr.TLSEnabled() {
		sslAnn, err := r.sslAnnotation(ctx, cr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get ssl annotations")
		}
		for k, v := range sslAnn {
			sfsSpec.Template.Annotations[k] = v
		}
	}

	return sfs, nil
}

func (r *ReconcilePerconaServerMongoDB) reconcilePVCs(ctx context.Context, sts *appsv1.StatefulSet, ls map[string]string, pvcSpec api.PVCSpec) error {
	pvcList := &corev1.PersistentVolumeClaimList{}
	err := r.client.List(ctx, pvcList, &client.ListOptions{
		Namespace:     sts.Namespace,
		LabelSelector: labels.SelectorFromSet(ls),
	})
	if err != nil {
		return errors.Wrap(err, "list PVCs")
	}

	for _, pvc := range pvcList.Items {
		orig := pvc.DeepCopy()

		for k, v := range pvcSpec.Labels {
			pvc.Labels[k] = v
		}
		for k, v := range sts.Labels {
			pvc.Labels[k] = v
		}
		for k, v := range pvcSpec.Annotations {
			pvc.Annotations[k] = v
		}

		if util.MapEqual(orig.Labels, pvc.Labels) && util.MapEqual(orig.Annotations, pvc.Annotations) {
			continue
		}
		patch := client.MergeFrom(orig)

		if err := r.client.Patch(ctx, &pvc, patch); err != nil {
			logf.FromContext(ctx).Error(err, "patch PVC", "PVC", pvc.Name)
		}
	}

	return nil
}
