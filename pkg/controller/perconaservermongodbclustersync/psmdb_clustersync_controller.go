package perconaservermongodbclustersync

import (
	"context"
	stderrors "errors"
	"fmt"
	"time"

	clustersyncclient "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/clustersync/client"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/clustersync"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

const (
	requeueInterval = 5 * time.Second
)

// Add wires the ClusterSync controller into the manager.
func Add(mgr manager.Manager) error {
	r, err := newReconciler(mgr)
	if err != nil {
		return err
	}
	return add(mgr, r)
}

func newReconciler(mgr manager.Manager) (reconcile.Reconciler, error) {
	cli, err := clientcmd.NewClient(mgr.GetConfig())
	if err != nil {
		return nil, errors.Wrap(err, "create clientcmd")
	}
	r := &ReconcilePerconaServerMongoDBClusterSync{
		client:               mgr.GetClient(),
		scheme:               mgr.GetScheme(),
		clientcmd:            cli,
		recorder:             mgr.GetEventRecorderFor("psmdbclustersync-controller"),
		newTargetMongoClient: defaultTargetMongoClient,
	}
	r.newPCSMClientFor = func(cr *psmdbv1.PerconaServerMongoDBClusterSync) pcsmClient {
		return clustersyncclient.New(r.client, r.clientcmd, cr)
	}
	return r, nil
}

func add(mgr manager.Manager, r reconcile.Reconciler) error {
	return builder.ControllerManagedBy(mgr).
		Named("psmdbclustersync-controller").
		For(&psmdbv1.PerconaServerMongoDBClusterSync{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

var _ reconcile.Reconciler = &ReconcilePerconaServerMongoDBClusterSync{}

type pcsmClient interface {
	Status(ctx context.Context) (clustersyncclient.Status, error)
	Start(ctx context.Context, opts clustersyncclient.StartOptions) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context, fromFailure bool) error
	Finalize(ctx context.Context) error
}

type ReconcilePerconaServerMongoDBClusterSync struct {
	client    client.Client
	scheme    *runtime.Scheme
	clientcmd *clientcmd.Client
	recorder  record.EventRecorder

	newPCSMClientFor     func(*psmdbv1.PerconaServerMongoDBClusterSync) pcsmClient
	newTargetMongoClient targetMongoClientFn
}

func (r *ReconcilePerconaServerMongoDBClusterSync) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := logf.FromContext(ctx)

	cr := &psmdbv1.PerconaServerMongoDBClusterSync{}
	if err := r.client.Get(ctx, request.NamespacedName, cr); err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if !cr.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, cr)
	}
	
	if cr.Status.State == psmdbv1.ClusterSyncStateFinalized {
		if err := r.releaseClusterSyncLease(ctx, cr); err != nil {
			return reconcile.Result{}, errors.Wrap(err, "release clustersync lease after finalize")
		}
		return reconcile.Result{}, nil
	}

	target := &psmdbv1.PerconaServerMongoDB{}
	targetNN := types.NamespacedName{Name: cr.Spec.ClusterName, Namespace: cr.Namespace}
	if err := r.client.Get(ctx, targetNN, target); err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("target cluster not found, surfacing in status", "target", targetNN)
			return r.requeueWithStatusError(ctx, cr, errors.Wrapf(err, "target cluster %s not found", targetNN))
		}
		return reconcile.Result{}, errors.Wrapf(err, "get target cluster %s", targetNN)
	}

	// Announce ownership of the target cluster before the PCSM
	// deployment exists. Backups/restores in the same namespace will
	// see this lease and stay in Waiting. The finalizer guarantees the
	// lease is released even if the CR is force-deleted.
	if err := r.ensureReleaseLockFinalizer(ctx, cr); err != nil {
		return reconcile.Result{}, errors.Wrapf(err, "ensure %s finalizer", naming.FinalizerReleaseLock)
	}
	foreignHolder, err := r.acquireClusterSyncLease(ctx, cr)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "acquire clustersync lease")
	}
	if foreignHolder != "" {
		log.Info("ClusterSync lease held by another CR, holding reconcile",
			"lease", naming.ClusterSyncLeaseName(cr.Spec.ClusterName), "holder", foreignHolder)
		return r.requeueWithStatusError(ctx, cr, errors.Errorf(
			"clustersync lease %s held by %q, not by this CR",
			naming.ClusterSyncLeaseName(cr.Spec.ClusterName), foreignHolder))
	}

	svr, err := version.Server(r.clientcmd)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "fetch server version")
	}
	if err := target.CheckNSetDefaults(ctx, svr.Platform); err != nil {
		return reconcile.Result{}, errors.Wrapf(err, "apply defaults to target cluster %s", targetNN)
	}

	sourceURI, err := buildSourceURI(ctx, r.client, cr)
	if err != nil {
		if k8serrors.IsNotFound(errors.Cause(err)) {
			log.Info("source credentials secret not yet available, surfacing in status",
				"secret", cr.Spec.Source.CredentialsSecret)
			return r.requeueWithStatusError(ctx, cr, errors.Wrapf(err, "source credentials secret %s not found", cr.Spec.Source.CredentialsSecret))
		}
		return reconcile.Result{}, errors.Wrap(err, "build source URI")
	}

	targetCreds, err := r.ensureSyncTargetUser(ctx, cr, target)
	if err != nil {
		log.Info("ensure sync target user failed, surfacing in status", "err", err.Error())
		return r.requeueWithStatusError(ctx, cr, errors.Wrap(err, "ensure sync target user"))
	}

	targetURI, err := buildTargetURI(target, targetCreds.Username, targetCreds.Password)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "build target URI")
	}

	if err := r.reconcileURISecret(ctx, cr, sourceURI, targetURI); err != nil {
		return reconcile.Result{}, errors.Wrap(err, "reconcile uri secret")
	}

	if err := r.reconcileDeployment(ctx, cr); err != nil {
		return reconcile.Result{}, errors.Wrap(err, "reconcile deployment")
	}

	ready, err := r.deploymentReady(ctx, cr)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "check deployment readiness")
	}
	if !ready {
		log.V(1).Info("PCSM deployment not ready yet", "name", clustersync.DeploymentName(cr))
		return reconcile.Result{RequeueAfter: requeueInterval}, nil
	}

	if err := r.reconcileMode(ctx, cr, r.newPCSMClientFor(cr)); err != nil {
		return reconcile.Result{}, errors.Wrap(err, "reconcile mode")
	}

	// PCSM has stopped replicating; the cluster is free again. We keep
	// the finalizer (released on CR delete) but drop the lease so
	// backups/restores can proceed without waiting for the user to
	// delete the CR.
	if cr.Status.State == psmdbv1.ClusterSyncStateFinalized {
		if err := r.releaseClusterSyncLease(ctx, cr); err != nil {
			return reconcile.Result{}, errors.Wrap(err, "release clustersync lease after finalize")
		}
	}

	log.V(1).Info("Reconciled ClusterSync", "clusterName", cr.Spec.ClusterName, "mode", cr.Spec.Mode, "state", cr.Status.State)
	return reconcile.Result{RequeueAfter: requeueInterval}, nil
}

func (r *ReconcilePerconaServerMongoDBClusterSync) handleDeletion(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync) (reconcile.Result, error) {
	log := logf.FromContext(ctx)

	hasFinalizer := false
	for _, f := range cr.GetFinalizers() {
		if f == naming.FinalizerReleaseLock {
			hasFinalizer = true
			break
		}
	}
	if !hasFinalizer {
		return reconcile.Result{}, nil
	}

	if err := r.releaseClusterSyncLease(ctx, cr); err != nil {
		log.Error(err, "release clustersync lease on delete")
		return reconcile.Result{RequeueAfter: requeueInterval}, nil
	}

	orig := cr.DeepCopy()
	finalizers := make([]string, 0, len(cr.GetFinalizers()))
	for _, f := range cr.GetFinalizers() {
		if f == naming.FinalizerReleaseLock {
			continue
		}
		finalizers = append(finalizers, f)
	}
	cr.SetFinalizers(finalizers)
	if err := r.client.Patch(ctx, cr, client.MergeFrom(orig)); err != nil {
		return reconcile.Result{}, errors.Wrap(err, "remove release-lock finalizer")
	}
	return reconcile.Result{}, nil
}

func (r *ReconcilePerconaServerMongoDBClusterSync) ensureReleaseLockFinalizer(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync) error {
	for _, f := range cr.GetFinalizers() {
		if f == naming.FinalizerReleaseLock {
			return nil
		}
	}
	orig := cr.DeepCopy()
	cr.SetFinalizers(append(cr.GetFinalizers(), naming.FinalizerReleaseLock))
	if err := r.client.Patch(ctx, cr.DeepCopy(), client.MergeFrom(orig)); err != nil {
		return errors.Wrap(err, "patch finalizers")
	}
	logf.FromContext(ctx).V(1).Info("Added finalizer", "finalizer", naming.FinalizerReleaseLock)
	return nil
}

// writeAction reports whether the given mode action causes PCSM to
// write to the target cluster. Start kicks off the initial sync;
// Resume restarts change-stream application. Pause and Finalize stop
// or drain PCSM and are always safe to run during a backup.
func writeAction(a modeAction) bool {
	return a == actionStart || a == actionResume
}

// clusterBusyByBackupOrRestore returns true if a backup or restore is
// currently in-flight against the target cluster. Reads cover both
// signals because they live in different places: backups acquire the
// backup lease (so we check that), restores do not (they coordinate
// through PBM's internal locks), so we list PerconaServerMongoDBRestore
// CRs in the namespace and look for non-terminal states.
func (r *ReconcilePerconaServerMongoDBClusterSync) clusterBusyByBackupOrRestore(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync) (bool, string, error) {
	backupLease := naming.BackupLeaseName(cr.Spec.ClusterName)
	active, err := k8s.IsLeaseActive(ctx, r.client, backupLease, cr.Namespace)
	if err != nil {
		return false, "", errors.Wrap(err, "check backup lease")
	}
	if active {
		return true, fmt.Sprintf("backup is in progress on cluster %q (lease %s)", cr.Spec.ClusterName, backupLease), nil
	}

	restores := &psmdbv1.PerconaServerMongoDBRestoreList{}
	if err := r.client.List(ctx, restores, client.InNamespace(cr.Namespace)); err != nil {
		return false, "", errors.Wrap(err, "list restores")
	}
	for i := range restores.Items {
		rst := &restores.Items[i]
		if rst.Spec.ClusterName != cr.Spec.ClusterName {
			continue
		}
		if restoreInFlight(rst.Status.State) {
			return true, fmt.Sprintf("restore %q is in progress on cluster %q (state=%s)",
				rst.Name, cr.Spec.ClusterName, rst.Status.State), nil
		}
	}
	return false, "", nil
}

func restoreInFlight(s psmdbv1.RestoreState) bool {
	switch s {
	case psmdbv1.RestoreStateReady, psmdbv1.RestoreStateError, psmdbv1.RestoreStateRejected:
		return false
	}
	return true
}

func (r *ReconcilePerconaServerMongoDBClusterSync) acquireClusterSyncLease(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync) (string, error) {
	lease, err := k8s.AcquireLease(ctx, r.client,
		naming.ClusterSyncLeaseName(cr.Spec.ClusterName), cr.Namespace,
		naming.ClusterSyncHolderId(cr))
	if err != nil {
		return "", err
	}
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != naming.ClusterSyncHolderId(cr) {
		holder := "<unknown>"
		if lease.Spec.HolderIdentity != nil {
			holder = *lease.Spec.HolderIdentity
		}
		return holder, nil
	}
	return "", nil
}

func (r *ReconcilePerconaServerMongoDBClusterSync) releaseClusterSyncLease(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync) error {
	err := k8s.ReleaseLease(ctx, r.client,
		naming.ClusterSyncLeaseName(cr.Spec.ClusterName), cr.Namespace,
		naming.ClusterSyncHolderId(cr))
	if k8serrors.IsNotFound(errors.Cause(err)) {
		return nil
	}
	// ErrNotTheHolder means the lease exists but belongs to a different
	// ClusterSync CR (e.g., a previous one with the same clusterName
	// was deleted and a new one was created before the lease GC ran).
	// Treat as success — we don't own it, nothing to release.
	if stderrors.Is(err, k8s.ErrNotTheHolder) {
		return nil
	}
	return err
}

func (r *ReconcilePerconaServerMongoDBClusterSync) reconcileDeployment(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync) error {
	dep := clustersync.Deployment(cr)
	if err := r.client.Get(ctx, types.NamespacedName{Name: dep.Name, Namespace: dep.Namespace}, dep); client.IgnoreNotFound(err) != nil {
		return errors.Wrap(err, "get clustersync deployment")
	}

	if err := controllerutil.SetControllerReference(cr, dep, r.scheme); err != nil {
		return errors.Wrap(err, "set owner reference on clustersync deployment")
	}

	dep.Labels = clustersync.Labels(cr)
	dep.Spec = clustersync.DeploymentSpec(cr, clustersync.PodTemplateSpec(cr))

	if _, err := util.Apply(ctx, r.client, dep); err != nil {
		return errors.Wrap(err, "apply clustersync deployment")
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDBClusterSync) reconcileURISecret(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync, sourceURI, targetURI string) error {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clustersync.URISecretName(cr),
			Namespace: cr.Namespace,
			Labels:    clustersync.Labels(cr),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			clustersync.URISecretSourceKey: []byte(sourceURI),
			clustersync.URISecretTargetKey: []byte(targetURI),
		},
	}
	if err := controllerutil.SetControllerReference(cr, s, r.scheme); err != nil {
		return errors.Wrap(err, "set owner reference on clustersync uri secret")
	}
	if _, err := util.Apply(ctx, r.client, s); err != nil {
		return errors.Wrap(err, "apply clustersync uri secret")
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDBClusterSync) deploymentReady(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync) (bool, error) {
	dep := &appsv1.Deployment{}
	nn := types.NamespacedName{Name: clustersync.DeploymentName(cr), Namespace: cr.Namespace}
	if err := r.client.Get(ctx, nn, dep); err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if dep.Spec.Replicas == nil {
		return false, nil
	}
	return dep.Generation == dep.Status.ObservedGeneration &&
		dep.Status.UpdatedReplicas == *dep.Spec.Replicas &&
		dep.Status.ReadyReplicas == dep.Status.UpdatedReplicas &&
		dep.Status.UnavailableReplicas == 0, nil
}

func (r *ReconcilePerconaServerMongoDBClusterSync) reconcileMode(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync, pcsm pcsmClient) error {
	log := logf.FromContext(ctx)
	newStatus := cr.Status.DeepCopy()

	observed, statusErr := pcsm.Status(ctx)
	if statusErr != nil {
		newStatus.Error = statusErr.Error()
		if isPCSMUnreachable(statusErr) {
			log.V(1).Info("PCSM CLI not reachable", "err", statusErr.Error())
			return r.writeStatus(ctx, cr, *newStatus)
		}
		log.Error(statusErr, "pcsm status failed")
	} else {
		applyObservedStatus(newStatus, observed)
	}

	action, mirror := nextAction(newStatus.Mode, cr.Spec.Mode, newStatus.State, newStatus.StartedAt != nil)
	if action != actionNone {
		if skipAction(action, newStatus.State) {
			log.Info("PCSM already in matching state, skipping transition",
				"action", action, "state", newStatus.State, "to", cr.Spec.Mode)
		} else {
			// Block actions that cause PCSM to write to the target while
			// a backup/restore is in flight on that cluster: their cursors
			// pin WiredTiger history and PCSM writes would race with PBM.
			if writeAction(action) {
				busy, reason, err := r.clusterBusyByBackupOrRestore(ctx, cr)
				if err != nil {
					return errors.Wrap(err, "check cluster busy by backup or restore")
				}
				if busy {
					newStatus.Error = reason
					log.Info("Holding PCSM transition: cluster busy with backup or restore",
						"action", action, "reason", reason)
					r.recorder.Eventf(cr, corev1.EventTypeNormal, "ClusterBusy",
						"Holding PCSM %q: %s", action, reason)
					return r.writeStatus(ctx, cr, *newStatus)
				}
			}
			if err := invokeAction(ctx, pcsm, action, cr, newStatus.State); err != nil {
				newStatus.Error = err.Error()
				if isPCSMUnreachable(err) {
					log.V(1).Info("PCSM CLI not reachable during transition", "action", action, "err", err.Error())
				} else {
					log.Error(err, "PCSM transition failed", "from", newStatus.Mode, "to", cr.Spec.Mode, "action", action)
				}
				return r.writeStatus(ctx, cr, *newStatus)
			}
			log.Info("PCSM transition applied", "from", newStatus.Mode, "to", cr.Spec.Mode, "action", action)
		}
	}
	if mirror {
		newStatus.Mode = cr.Spec.Mode
	}

	return r.writeStatus(ctx, cr, *newStatus)
}

func isPCSMUnreachable(err error) bool {
	return stderrors.Is(err, clustersyncclient.ErrPCSMNotReady)
}

func applyObservedStatus(s *psmdbv1.PerconaServerMongoDBClusterSyncStatus, observed clustersyncclient.Status) {
	s.State = psmdbv1.ClusterSyncState(observed.State)
	s.LagTimeSeconds = observed.LagTimeSeconds
	s.Error = observed.Error

	if s.StartedAt == nil && s.State == psmdbv1.ClusterSyncStateRunning {
		now := metav1.Now()
		s.StartedAt = &now
	}

	runningStatus := metav1.ConditionFalse
	runningReason := "PCSMNotRunning"
	runningMsg := fmt.Sprintf("PCSM state: %s", s.State)
	if s.State == psmdbv1.ClusterSyncStateRunning {
		runningStatus = metav1.ConditionTrue
		runningReason = "PCSMRunning"
		runningMsg = "PCSM is replicating from source to target"
	}
	meta.SetStatusCondition(&s.Conditions, metav1.Condition{
		Type:    psmdbv1.ConditionClusterSyncRunning,
		Status:  runningStatus,
		Reason:  runningReason,
		Message: runningMsg,
	})

	if s.State == psmdbv1.ClusterSyncStateFinalized {
		meta.SetStatusCondition(&s.Conditions, metav1.Condition{
			Type:    psmdbv1.ConditionClusterSyncFinalized,
			Status:  metav1.ConditionTrue,
			Reason:  "PCSMFinalized",
			Message: "PCSM finalize complete",
		})
	}
}

func invokeAction(ctx context.Context, pcsm pcsmClient, action modeAction, cr *psmdbv1.PerconaServerMongoDBClusterSync, state psmdbv1.ClusterSyncState) error {
	switch action {
	case actionStart:
		return pcsm.Start(ctx, clustersyncclient.StartOptions{ExcludeNamespaces: cr.Spec.ExcludeNamespaces})
	case actionResume:
		return pcsm.Resume(ctx, state == psmdbv1.ClusterSyncStateFailed)
	case actionPause:
		return pcsm.Pause(ctx)
	case actionFinalize:
		return pcsm.Finalize(ctx)
	}
	return nil
}

// requeueWithStatusError records a precondition failure (missing target,
// missing source secret, target mongo unreachable) into status.Error and
// requeues. Returning a controller-runtime error instead would back off
// silently without giving the user any visibility into why the CR is
// stuck — these failures are external-state issues the user is expected
// to resolve out-of-band, so the status must reflect them.
func (r *ReconcilePerconaServerMongoDBClusterSync) requeueWithStatusError(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync, cause error) (reconcile.Result, error) {
	newStatus := cr.Status.DeepCopy()
	newStatus.Error = cause.Error()
	if err := r.writeStatus(ctx, cr, *newStatus); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{RequeueAfter: requeueInterval}, nil
}

func (r *ReconcilePerconaServerMongoDBClusterSync) writeStatus(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync, newStatus psmdbv1.PerconaServerMongoDBClusterSyncStatus) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		c := &psmdbv1.PerconaServerMongoDBClusterSync{}
		if err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, c); err != nil {
			return err
		}
		c.Status = newStatus
		return r.client.Status().Update(ctx, c)
	})
	if k8serrors.IsNotFound(err) {
		return nil
	}
	return errors.Wrap(err, "write status")
}
