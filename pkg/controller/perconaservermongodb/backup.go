package perconaservermongodb

import (
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/pbm"
)

func (r *ReconcilePerconaServerMongoDB) isRestoreRunning(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	restores := api.PerconaServerMongoDBRestoreList{}
	if err := r.client.List(ctx, &restores, &client.ListOptions{Namespace: cr.Namespace}); err != nil {
		if k8sErrors.IsNotFound(err) {
			return false, nil
		}

		return false, errors.Wrap(err, "get restore list")
	}

	for _, rst := range restores.Items {
		if rst.Status.State != api.RestoreStateReady &&
			rst.Status.State != api.RestoreStateError &&
			rst.Spec.ClusterName == cr.Name {
			return true, nil
		}
	}

	return false, nil
}

func (r *ReconcilePerconaServerMongoDB) isBackupRunning(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	bcps := api.PerconaServerMongoDBBackupList{}
	if err := r.client.List(ctx, &bcps, &client.ListOptions{Namespace: cr.Namespace}); err != nil {
		if k8sErrors.IsNotFound(err) {
			return false, nil
		}
		return false, errors.Wrap(err, "get backup list")
	}

	for _, bcp := range bcps.Items {
		if bcp.Status.State != api.BackupStateReady &&
			bcp.Status.State != api.BackupStateError &&
			bcp.Spec.GetClusterName() == cr.Name {
			return true, nil
		}
	}

	return false, nil
}

func (r *ReconcilePerconaServerMongoDB) hasFullBackup(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	backups := api.PerconaServerMongoDBBackupList{}
	if err := r.client.List(ctx, &backups, &client.ListOptions{Namespace: cr.Namespace}); err != nil {
		if k8sErrors.IsNotFound(err) {
			return false, nil
		}
		return false, errors.Wrap(err, "get backup list")
	}

	for _, b := range backups.Items {
		if b.Status.State == api.BackupStateReady && b.Spec.GetClusterName() == cr.Name {
			return true, nil
		}
	}

	return false, nil
}

func (r *ReconcilePerconaServerMongoDB) resyncPBMIfNeeded(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	if cr.Status.State != api.AppStateReady || !cr.Spec.Backup.Enabled {
		return nil
	}

	_, resyncNeeded := cr.Annotations[api.AnnotationResyncPBM]
	if !resyncNeeded {
		return nil
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		c := &api.PerconaServerMongoDB{}
		err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, c)
		if err != nil {
			return err
		}

		orig := c.DeepCopy()
		delete(c.Annotations, api.AnnotationResyncPBM)

		return r.client.Patch(ctx, c, client.MergeFrom(orig))
	})
	if err != nil {
		return errors.Wrap(err, "delete annotation")
	}

	log.V(1).Info("Deleted annotation", "annotation", api.AnnotationResyncPBM)

	pod, err := psmdb.GetOneReadyRSPod(ctx, r.client, cr, cr.Spec.Replsets[0].Name)
	if err != nil {
		return errors.Wrapf(err, "get a pod from rs/%s", cr.Spec.Replsets[0].Name)
	}

	log.Info("Starting PBM resync", "pod", pod.Name)
	if err := pbm.ForceResync(ctx, r.clientcmd, pod); err != nil {
		return errors.Wrap(err, "force PBM resync")

	}

	return nil
}

func secretExists(ctx context.Context, cl client.Client, nn types.NamespacedName) (bool, error) {
	var secret corev1.Secret
	err := cl.Get(ctx, nn, &secret)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func (r *ReconcilePerconaServerMongoDB) reconcilePBMConfiguration(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx).WithName("reconcilePBMConfiguration")

	if !cr.Spec.Backup.Enabled {
		return nil
	}

	if cr.Spec.Backup.Storages == nil {
		log.Info("Waiting for backup storages to be configured")
		return nil
	}

	for _, rs := range cr.Spec.Replsets {
		restoreRunning, err := r.restoreInProgress(ctx, cr, rs)
		if err != nil {
			return errors.Wrap(err, "check if restore is running")
		}

		if restoreRunning {
			log.Info("Waiting for restore to complete")
			return nil
		}
	}

	pod, err := psmdb.GetOneReadyRSPod(ctx, r.client, cr, cr.Spec.Replsets[0].Name)
	if err != nil {
		log.Info("Waiting for a ready pod")
		return nil
	}

	hasRunning, err := pbm.HasRunningOperation(ctx, r.clientcmd, pod)
	if err != nil && !pbm.IsNotConfigured(err) {
		return errors.Wrap(err, "check if PBM has running operation")
	}

	if hasRunning {
		log.V(1).Info("PBM has running operation")
		return nil
	}

	if len(cr.Status.BackupStorage) == 0 {
		return nil
	}

	_, ok := cr.Spec.Backup.Storages[cr.Status.BackupStorage]
	if !ok {
		return nil
	}

	if err := pbm.CreateOrUpdateConfig(ctx, r.clientcmd, r.client, cr); err != nil {
		return errors.Wrap(err, "create or update PBM configuration")
	}

	// Initialize PBM configuration
	if cond := meta.FindStatusCondition(cr.Status.Conditions, "PBMReady"); cond != nil && cond.Status != metav1.ConditionTrue && cond.Reason == "PBMIsNotConfigured" {
		if !pbm.FileExists(ctx, r.clientcmd, pod, pbm.GetConfigPathForStorage(cr.Status.BackupStorage)) {
			log.Info("Waiting for PBM configuration to be propagated to the pod")
			return nil
		}

		if err := pbm.SetConfigFile(ctx, r.clientcmd, pod, pbm.GetConfigPathForStorage(cr.Status.BackupStorage)); err != nil {
			return errors.Wrapf(err, "set PBM config file %s", pbm.ConfigFileDir+"/"+cr.Status.BackupStorage)
		}

		return nil
	}

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-pbm-config",
			Namespace: cr.Namespace,
		},
	}
	err = r.client.Get(ctx, client.ObjectKeyFromObject(&secret), &secret)
	if err != nil {
		return errors.Wrapf(err, "get secret %s", client.ObjectKeyFromObject(&secret))
	}

	_, ok = secret.Annotations["percona.com/config-applied"]
	if ok {
		return nil
	}

	checksum, ok := secret.Annotations["percona.com/config-sum"]
	if !ok {
		return errors.New("missing checksum annotation")
	}

	// is secret propagated to the pod
	if !pbm.CheckSHA256Sum(ctx, r.clientcmd, pod, checksum) {
		log.Info("Waiting for PBM configuration to be propagated to the pod")
		return nil
	}

	log.V(1).Info("PBM configuration is propagated to the pod", "pod", pod.Name)

	if err := pbm.SetConfigFile(ctx, r.clientcmd, pod, pbm.GetConfigPathForStorage(cr.Status.BackupStorage)); err != nil {
		return errors.Wrapf(err, "set PBM config file %s", pbm.GetConfigPathForStorage(cr.Status.BackupStorage))
	}

	log.V(1).Info("PBM configuration is applied to the DB", "pod", pod.Name)
	secret.Annotations["percona.com/config-applied"] = "true"

	return r.client.Update(ctx, &secret)
}
