package perconaservermongodbclustersync

import (
	"context"
	stderrors "errors"
	"fmt"
	"slices"
	"time"

	clustersyncclient "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/clustersync/client"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/equality"
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
func Add(ctx context.Context, mgr manager.Manager) error {
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
		return clustersyncclient.New(r.client, r.clientcmd, cr).WithActivePod(cr.Status.ActiveInstance)
	}
	return r, nil
}

func add(mgr manager.Manager, r reconcile.Reconciler) error {
	return builder.ControllerManagedBy(mgr).
		Named("psmdbclustersync-controller").
		For(&psmdbv1.PerconaServerMongoDBClusterSync{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Secret{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Complete(r)
}

var _ reconcile.Reconciler = &ReconcilePerconaServerMongoDBClusterSync{}

type pcsmClient interface {
	Status(ctx context.Context) (clustersyncclient.Status, error)
	Start(ctx context.Context, opts clustersyncclient.StartOptions) error
	Pause(ctx context.Context) error
	Resume(ctx context.Context, fromFailure bool) error
	Finalize(ctx context.Context) error
	ActiveInstance() string
}

type ReconcilePerconaServerMongoDBClusterSync struct {
	client    client.Client
	scheme    *runtime.Scheme
	clientcmd clientcmd.Client
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

	// Gate on spec.Mode so a new CR inheriting finalized state from a
	// previous sync on the same target doesn't short-circuit.
	if cr.Spec.Mode == psmdbv1.ClusterSyncModeFinalized && cr.Status.State == psmdbv1.ClusterSyncStateFinalized {
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

	svr, err := version.Server(ctx, r.clientcmd)
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

	if err := r.reconcilePDB(ctx, cr); err != nil {
		return reconcile.Result{}, errors.Wrap(err, "reconcile pod disruption budget")
	}

	ready, err := r.readyReplicas(ctx, cr)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "check deployment readiness")
	}
	if ready == 0 {
		log.V(1).Info("PCSM deployment not ready yet", "name", clustersync.DeploymentName(cr))
		newStatus := cr.Status.DeepCopy()
		newStatus.Size = clustersync.Size(cr)
		newStatus.ReadyReplicas = 0
		if err := r.writeStatus(ctx, cr, *newStatus); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{RequeueAfter: requeueInterval}, nil
	}

	if err := r.reconcileMode(ctx, cr, r.newPCSMClientFor(cr), ready); err != nil {
		return reconcile.Result{}, errors.Wrap(err, "reconcile mode")
	}

	// Drop the lease once the user-requested finalize completes so
	// backups/restores don't wait on CR deletion. Same gate as the
	// early-exit above.
	if cr.Spec.Mode == psmdbv1.ClusterSyncModeFinalized && cr.Status.State == psmdbv1.ClusterSyncStateFinalized {
		if err := r.releaseClusterSyncLease(ctx, cr); err != nil {
			return reconcile.Result{}, errors.Wrap(err, "release clustersync lease after finalize")
		}
	}

	log.V(1).Info("Reconciled ClusterSync", "clusterName", cr.Spec.ClusterName, "mode", cr.Spec.Mode, "state", cr.Status.State)
	return reconcile.Result{RequeueAfter: requeueInterval}, nil
}

func (r *ReconcilePerconaServerMongoDBClusterSync) handleDeletion(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync) (reconcile.Result, error) {
	log := logf.FromContext(ctx)

	hasFinalizer := slices.Contains(cr.GetFinalizers(), naming.FinalizerReleaseLock)
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
	if slices.Contains(cr.GetFinalizers(), naming.FinalizerReleaseLock) {
		return nil
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
		naming.ClusterSyncHolderId(cr), nil)
	if err != nil {
		if stderrors.Is(err, k8s.ErrLeaseAlreadyHeld) {
			leaseName := naming.ClusterSyncLeaseName(cr.Spec.ClusterName)
			lease, err := k8s.GetLease(ctx, r.client, leaseName, cr.Namespace)
			if err != nil {
				return "", err
			}
			holder := "<unknown>"
			if lease.Spec.HolderIdentity != nil {
				holder = *lease.Spec.HolderIdentity
			}
			return holder, nil
		}
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
		Name:      clustersync.URISecretName(cr),
		Namespace: cr.Namespace,
		Labels:    clustersync.Labels(cr),
		Type:      corev1.SecretTypeOpaque,
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

// readyReplicas reports how many PCSM instances are ready. A single ready
// instance is enough to drive PCSM: in an HA group the others are
// standbys, so waiting for all of them would let one sick standby freeze
// a healthy sync. The shortfall is reported in status instead.
func (r *ReconcilePerconaServerMongoDBClusterSync) readyReplicas(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync) (int32, error) {
	dep := &appsv1.Deployment{}
	nn := types.NamespacedName{Name: clustersync.DeploymentName(cr), Namespace: cr.Namespace}
	if err := r.client.Get(ctx, nn, dep); err != nil {
		if k8serrors.IsNotFound(err) {
			return 0, nil
		}
		return 0, err
	}
	if dep.Generation != dep.Status.ObservedGeneration {
		return 0, nil
	}
	return dep.Status.ReadyReplicas, nil
}

// reconcilePDB removes the budget when the group shrinks back to a single
// instance; leaving a stale PDB behind would block node drains forever.
func (r *ReconcilePerconaServerMongoDBClusterSync) reconcilePDB(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync) error {
	pdb := clustersync.PodDisruptionBudget(cr)
	if pdb == nil {
		// Read first: this runs on every requeue, and an unconditional
		// Delete would be a write call against the API server each time.
		stale := &policyv1.PodDisruptionBudget{}
		nn := types.NamespacedName{Name: clustersync.DeploymentName(cr), Namespace: cr.Namespace}
		if err := r.client.Get(ctx, nn, stale); err != nil {
			return client.IgnoreNotFound(err)
		}
		logf.FromContext(ctx).Info("Removing PCSM pod disruption budget", "name", nn.Name)
		return client.IgnoreNotFound(r.client.Delete(ctx, stale))
	}

	if err := controllerutil.SetControllerReference(cr, pdb, r.scheme); err != nil {
		return errors.Wrap(err, "set owner reference on clustersync pdb")
	}
	if _, err := util.Apply(ctx, r.client, pdb); err != nil {
		return errors.Wrap(err, "apply clustersync pdb")
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDBClusterSync) reconcileMode(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBClusterSync, pcsm pcsmClient, ready int32) error {
	log := logf.FromContext(ctx)
	newStatus := cr.Status.DeepCopy()
	newStatus.Size = clustersync.Size(cr)
	newStatus.ReadyReplicas = ready

	observed, statusErr := pcsm.Status(ctx)
	r.recordActiveInstance(cr, newStatus, pcsm.ActiveInstance())
	if statusErr != nil {
		newStatus.Error = statusErr.Error()
		if isPCSMUnreachable(statusErr) {
			log.V(1).Info("PCSM CLI not reachable", "err", statusErr.Error())
			return r.writeStatus(ctx, cr, *newStatus)
		}
		if alreadyReported(cr, newStatus.Error) {
			log.V(1).Info("pcsm status still failing", "err", newStatus.Error)
		} else {
			log.Error(statusErr, "pcsm status failed")
		}
	} else {
		applyObservedStatus(newStatus, observed, cr.Generation)
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
					// A backup can hold the cluster for hours and this
					// path runs every requeue, so report the hold once
					// per distinct reason and leave the rest to status.
					if alreadyReported(cr, reason) {
						log.V(1).Info("Still holding PCSM transition: cluster busy",
							"action", action, "reason", reason)
					} else {
						log.Info("Holding PCSM transition: cluster busy with backup or restore",
							"action", action, "reason", reason)
						r.recorder.Eventf(cr, corev1.EventTypeNormal, "ClusterBusy",
							"Holding PCSM %q: %s", action, reason)
					}
					newStatus.Error = reason
					return r.writeStatus(ctx, cr, *newStatus)
				}
			}
			if err := invokeAction(ctx, pcsm, action, cr, newStatus.State); err != nil {
				newStatus.Error = err.Error()
				switch {
				case isPCSMUnreachable(err):
					log.V(1).Info("PCSM CLI not reachable during transition", "action", action, "err", err.Error())
				case alreadyReported(cr, newStatus.Error):
					// The failure self-heal retries a failed resume every
					// requeue; a permanently failing one must not log an
					// error every few seconds.
					log.V(1).Info("PCSM transition still failing", "action", action, "err", newStatus.Error)
				default:
					log.Error(err, "PCSM transition failed", "from", newStatus.Mode, "to", cr.Spec.Mode, "action", action)
				}
				return r.writeStatus(ctx, cr, *newStatus)
			}
			r.recordActiveInstance(cr, newStatus, pcsm.ActiveInstance())
			log.Info("PCSM transition applied", "from", newStatus.Mode, "to", cr.Spec.Mode, "action", action)
		}
	}
	if mirror {
		newStatus.Mode = cr.Spec.Mode
	}

	return r.writeStatus(ctx, cr, *newStatus)
}

// alreadyReported reports whether this exact message is the one already
// persisted in status, i.e. it has been logged on an earlier pass.
func alreadyReported(cr *psmdbv1.PerconaServerMongoDBClusterSync, msg string) bool {
	return msg != "" && cr.Status.Error == msg
}

// recordActiveInstance tracks which pod holds the PCSM lease and reports
// handovers, the only externally visible sign that a failover happened.
func (r *ReconcilePerconaServerMongoDBClusterSync) recordActiveInstance(
	cr *psmdbv1.PerconaServerMongoDBClusterSync,
	newStatus *psmdbv1.PerconaServerMongoDBClusterSyncStatus,
	active string,
) {
	if active == "" {
		return
	}
	// Compare against newStatus, not cr.Status: this runs more than once
	// per reconcile and must not report the same handover twice.
	if prev := newStatus.ActiveInstance; prev != "" && prev != active {
		r.recorder.Eventf(cr, corev1.EventTypeNormal, "PCSMFailover",
			"Active PCSM instance changed from %q to %q", prev, active)
	}
	newStatus.ActiveInstance = active
}

// ErrNoActivePCSM means the group is mid-election; like a missing pod it
// is a transient condition, not a reconcile failure.
func isPCSMUnreachable(err error) bool {
	return stderrors.Is(err, clustersyncclient.ErrPCSMNotReady) ||
		stderrors.Is(err, clustersyncclient.ErrNoActivePCSM)
}

func applyObservedStatus(s *psmdbv1.PerconaServerMongoDBClusterSyncStatus, observed clustersyncclient.Status, generation int64) {
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
		Type:               psmdbv1.ConditionClusterSyncRunning,
		Status:             runningStatus,
		Reason:             runningReason,
		Message:            runningMsg,
		ObservedGeneration: generation,
	})

	if s.State == psmdbv1.ClusterSyncStateFinalized {
		meta.SetStatusCondition(&s.Conditions, metav1.Condition{
			Type:               psmdbv1.ConditionClusterSyncFinalized,
			Status:             metav1.ConditionTrue,
			Reason:             "PCSMFinalized",
			Message:            "PCSM finalize complete",
			ObservedGeneration: generation,
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
	// The Get stays inside the closure: re-reading is what makes the
	// retry resolve the conflict rather than resubmit the same stale
	// resourceVersion. Reconcile requeues every few seconds, so the
	// equality check keeps a steady-state CR from being written each pass.
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		c := &psmdbv1.PerconaServerMongoDBClusterSync{}
		if err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, c); err != nil {
			return err
		}
		if equality.Semantic.DeepEqual(c.Status, newStatus) {
			return nil
		}
		c.Status = newStatus
		return r.client.Status().Update(ctx, c)
	})
	if k8serrors.IsNotFound(err) {
		return nil
	}
	return errors.Wrap(err, "write status")
}
