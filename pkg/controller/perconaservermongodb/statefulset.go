package perconaservermongodb

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	psmdbconfig "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/logcollector"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/logcollector/logrotate"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/vectorsearch"
)

var errConfigNotInCache = errors.New("config map is not in the cache yet")

func (r *ReconcilePerconaServerMongoDB) reconcileStatefulSet(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, ls map[string]string) (*appsv1.StatefulSet, error) {
	log := logf.FromContext(ctx)

	if rs.ClusterRole == api.ClusterRoleConfigSvr {
		ls[naming.LabelKubernetesComponent] = naming.ComponentConfigSrv
	}

	sfs, err := r.getStatefulsetFromReplset(ctx, cr, rs, ls)
	if err != nil {
		return nil, errors.Wrapf(err, "get StatefulSet for replset %s", rs.Name)
	}

	if _, ok := sfs.Annotations[api.AnnotationRestoreInProgress]; ok {
		log.V(1).Info("Restore in progress, skipping reconciliation of statefulset", "name", sfs.Name)
		return sfs, nil
	}

	// During PVC shrink the STS was deleted (cascade=orphan) and must be
	// recreated with the new VolumeClaimTemplate before pods can be processed.
	// Ensure the STS exists first, then run the shrink state machine, and skip
	// smartUpdate/PDB/autoscaling which would conflict with the rolling process.
	if state, ok := cr.GetPVCShrinkState(sfs.Name); ok {
		target, err := resource.ParseQuantity(state.TargetSize)
		if err != nil {
			return nil, errors.Wrapf(err, "parse shrink target size of %s", sfs.Name)
		}
		claimName := dataClaimName(ls[naming.LabelKubernetesComponent])
		pinShrinkTarget(sfs, claimName, target)
		// The claim template can't be updated, a statefulset still having the old one is recreated.
		ready, err := r.shrinkClaimTemplateReady(ctx, cr, sfs, claimName, target)
		if err != nil {
			return nil, err
		}
		if !ready {
			return sfs, nil
		}

		err = r.createOrUpdate(ctx, sfs)
		if err != nil {
			return nil, errors.Wrapf(err, "update StatefulSet %s", sfs.Name)
		}

		if err := r.reconcilePVCs(ctx, cr, sfs); err != nil {
			return nil, errors.Wrapf(err, "reconcile PVCs for %s", sfs.Name)
		}

		log.V(1).Info("PVC shrink in progress, skipping smartUpdate and PDB reconciliation", "name", sfs.Name)
		return sfs, nil
	}

	if err := r.validateJournalStorage(ctx, sfs, journalStorage(cr, sfs) != nil); err != nil {
		return nil, err
	}

	recreated, err := r.recreateStsForStorageIfNeeded(ctx, cr, sfs)
	if err != nil {
		return nil, errors.Wrapf(err, "recreate StatefulSet %s for storage change", sfs.Name)
	}
	if recreated {
		return sfs, nil
	}

	if err := r.reconcilePVCs(ctx, cr, sfs); err != nil {
		return nil, errors.Wrapf(err, "reconcile PVCs for %s", sfs.Name)
	}

	// Re-read the StatefulSet from the cluster to check for the PVC resize
	// annotation, as it may have been set during reconcilePVCs and the local
	// sfs object would be stale.
	currentSts := new(appsv1.StatefulSet)
	err = r.client.Get(ctx, types.NamespacedName{Name: sfs.Name, Namespace: sfs.Namespace}, currentSts)
	switch {
	case err == nil:
		if _, ok := currentSts.Annotations[api.AnnotationPVCResizeInProgress]; ok {
			log.V(1).Info("PVC resize in progress, skipping reconciliation of statefulset", "name", sfs.Name)
			return sfs, nil
		}
	case k8sErrors.IsNotFound(err) && sfs.ResourceVersion != "":
		log.V(1).Info("statefulset deleted for PVC resize, recreating it on the next reconcile", "name", sfs.Name)
		return sfs, nil
	}

	delete(sfs.Annotations, api.AnnotationPVCResizeInProgress)

	err = r.createOrUpdate(ctx, sfs)
	if err != nil {
		return nil, errors.Wrapf(err, "update StatefulSet %s", sfs.Name)
	}

	pdbspec := rs.PodDisruptionBudget

	switch ls[naming.LabelKubernetesComponent] {
	case naming.ComponentSearch:
		return sfs, nil
	case naming.ComponentArbiter:
		pdbspec = rs.Arbiter.PodDisruptionBudget
	case naming.ComponentNonVoting:
		pdbspec = rs.NonVoting.PodDisruptionBudget
	case naming.ComponentHidden:
		pdbspec = rs.Hidden.PodDisruptionBudget
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

var errJournalStorageDisable = errors.New("journalStorage can't be disabled on a running statefulset")

func (r *ReconcilePerconaServerMongoDB) validateJournalStorage(ctx context.Context, sfs *appsv1.StatefulSet, wantJournal bool) error {
	if wantJournal {
		return nil
	}

	current := new(appsv1.StatefulSet)
	err := r.client.Get(ctx, types.NamespacedName{Name: sfs.Name, Namespace: sfs.Namespace}, current)
	if client.IgnoreNotFound(err) != nil {
		return errors.Wrapf(err, "get statefulset %s", sfs.Name)
	}

	var hasJournal bool
	if err == nil {
		hasJournal = k8s.HasVolumeClaimTemplate(current, psmdbconfig.MongodJournalVolClaimName)
	} else {
		// the journal moved off the data PVC, so retained journal PVCs still hold it
		hasJournal, err = r.hasJournalPVCs(ctx, sfs)
		if err != nil {
			return err
		}
	}
	if !hasJournal {
		return nil
	}

	return errors.Wrapf(errJournalStorageDisable, "statefulset %s", sfs.Name)
}

func (r *ReconcilePerconaServerMongoDB) hasJournalPVCs(ctx context.Context, sfs *appsv1.StatefulSet) (bool, error) {
	pvcList := new(corev1.PersistentVolumeClaimList)
	if err := r.client.List(ctx, pvcList, client.InNamespace(sfs.Namespace)); err != nil {
		return false, errors.Wrap(err, "list PVCs")
	}

	prefix := psmdbconfig.MongodJournalVolClaimName + "-" + sfs.Name + "-"
	return slices.ContainsFunc(pvcList.Items, func(pvc corev1.PersistentVolumeClaim) bool {
		ordinal, ok := strings.CutPrefix(pvc.Name, prefix)
		if !ok {
			return false
		}
		_, err := strconv.Atoi(ordinal)
		return err == nil
	}), nil
}

var errStorageChangeNeedsSmartUpdate = errors.New("adding or removing log or journal storage on running pods requires updateStrategy SmartUpdate")

func (r *ReconcilePerconaServerMongoDB) recreateStsForStorageIfNeeded(ctx context.Context, cr *api.PerconaServerMongoDB, sfs *appsv1.StatefulSet) (bool, error) {
	if cr.CompareVersion("1.24.0") < 0 {
		return false, nil
	}

	if sfs.Labels[naming.LabelKubernetesComponent] == naming.ComponentArbiter {
		return false, nil
	}

	current := new(appsv1.StatefulSet)
	if err := r.client.Get(ctx, types.NamespacedName{Name: sfs.Name, Namespace: sfs.Namespace}, current); err != nil {
		return false, client.IgnoreNotFound(err)
	}

	return r.recreateStsForClaims(ctx, cr, current, map[string]bool{
		psmdbconfig.MongodLogVolClaimName:     logStorage(cr, sfs) != nil,
		psmdbconfig.MongodJournalVolClaimName: journalStorage(cr, sfs) != nil,
	})
}

// recreateStsForClaims orphan-deletes current when a claim template has to be added or removed.
func (r *ReconcilePerconaServerMongoDB) recreateStsForClaims(ctx context.Context, cr *api.PerconaServerMongoDB, current *appsv1.StatefulSet, wantClaims map[string]bool) (bool, error) {
	// orphan deletion keeps the object until its finalizer is processed
	if current.DeletionTimestamp != nil {
		return true, nil
	}

	var changed []string
	for claim, want := range wantClaims {
		if k8s.HasVolumeClaimTemplate(current, claim) != want {
			changed = append(changed, claim)
		}
	}
	if len(changed) == 0 {
		return false, nil
	}
	slices.Sort(changed)

	// only SmartUpdate recreates pods that miss a new claim, the statefulset controller can't add volumes to them
	if cr.Spec.UpdateStrategy != api.SmartUpdateStatefulSetStrategyType && current.Status.Replicas > 0 {
		return false, errStorageChangeNeedsSmartUpdate
	}

	if err := k8s.OrphanDeleteStatefulSet(ctx, r.client, current, client.Preconditions{UID: &current.UID}); err != nil {
		if k8sErrors.IsNotFound(err) || k8sErrors.IsConflict(err) {
			return false, nil
		}
		return false, err
	}

	logf.FromContext(ctx).Info("Recreating statefulset to change storage", "statefulset", current.Name, "claims", changed)

	return true, nil
}

func (r *ReconcilePerconaServerMongoDB) getStatefulsetFromReplset(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, ls map[string]string) (*appsv1.StatefulSet, error) {
	if ls[naming.LabelKubernetesComponent] == naming.ComponentSearch {
		return r.getSearchStatefulSet(ctx, cr, rs)
	}

	sfsName := cr.Name + "-" + rs.Name
	mongodCustomConfigName := naming.MongodCustomConfigName(cr, rs)
	mongodConfiguration := string(rs.Configuration)

	if rs.ClusterRole == api.ClusterRoleConfigSvr {
		ls[naming.LabelKubernetesComponent] = api.ConfigReplSetName
	}

	switch ls[naming.LabelKubernetesComponent] {
	case naming.ComponentArbiter:
		sfsName += "-" + naming.ComponentArbiter
	case naming.ComponentNonVoting:
		sfsName += "-" + naming.ComponentNonVotingShort
		mongodCustomConfigName = naming.NonVotingConfigMapName(cr, rs)
		mongodConfiguration = string(rs.NonVoting.Configuration)
	case naming.ComponentHidden:
		sfsName += "-" + naming.ComponentHidden
		mongodCustomConfigName = naming.HiddenConfigMapName(cr, rs)
		mongodConfiguration = string(rs.Hidden.Configuration)
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

	// ConfigMap is created earlier and may not be in the cache yet
	if !mongodCustomConfig.Type.IsUsable() {
		// even if user doesn't have custom config, we might need to inject something for vector search
		mongodConfiguration, err = vectorsearch.InjectMongodConfig(mongodConfiguration, cr, rs)
		if err != nil {
			return nil, errors.Wrap(err, "inject search setParameters")
		}

		if mongodConfiguration != "" {
			err = retry.OnError(retry.DefaultBackoff, func(err error) bool {
				return errors.Is(err, errConfigNotInCache)
			}, func() error {
				mongodCustomConfig, err = r.getCustomConfig(ctx, cr.Namespace, mongodCustomConfigName)
				if err != nil {
					return err
				}
				if !mongodCustomConfig.Type.IsUsable() {
					return errConfigNotInCache
				}
				return nil
			})
			if err != nil {
				return nil, errors.Wrapf(err, "get mongod custom configuration %s", mongodCustomConfigName)
			}
		}
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
	maps.Copy(sfsSpec.Template.Annotations, sslAnn)

	if preservedValue, exists := cr.Annotations[api.AnnotationPreservedRestartedAt(sfs.Name)]; exists {
		sfsSpec.Template.Annotations[naming.AnnotationKubectlRestartedAt] = preservedValue
	}

	return sfs, nil
}
