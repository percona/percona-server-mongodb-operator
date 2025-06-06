package perconaservermongodb

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

func (r *ReconcilePerconaServerMongoDB) updateStatus(ctx context.Context, cr *api.PerconaServerMongoDB, reconcileErr error, clusterState api.AppState) error {
	if clusterState == api.AppStateNone {
		clusterState = api.AppStateInit
	}

	log := logf.FromContext(ctx)

	clusterCondition := api.ClusterCondition{
		Status:             api.ConditionTrue,
		Type:               api.AppStateInit,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}
	if reconcileErr != nil {
		if cr.Status.State != api.AppStateError {
			clusterCondition = api.ClusterCondition{
				Status:             api.ConditionTrue,
				Type:               api.AppStateError,
				Message:            reconcileErr.Error(),
				Reason:             "ErrorReconcile",
				LastTransitionTime: metav1.NewTime(time.Now()),
			}
			cr.Status.AddCondition(clusterCondition)
		}

		cr.Status.Message = "Error: " + reconcileErr.Error()
		cr.Status.State = api.AppStateError

		return r.writeStatus(ctx, cr)
	}

	cr.Status.Message = ""

	replsetsReady := 0
	replsetsStopping := 0
	replsetsPaused := 0
	inProgress := false

	repls := cr.Spec.Replsets
	if cr.Spec.Sharding.Enabled && cr.Spec.Sharding.ConfigsvrReplSet != nil {
		repls = append(repls, cr.Spec.Sharding.ConfigsvrReplSet)
	} else {
		delete(cr.Status.Replsets, api.ConfigReplSetName)
		for i := range cr.Status.Replsets {
			rs := cr.Status.Replsets[i]
			rs.AddedAsShard = nil
			cr.Status.Replsets[i] = rs
		}
	}

	leftRsStatuses := make(map[string]api.ReplsetStatus)
	for _, repl := range repls {
		if v, ok := cr.Status.Replsets[repl.Name]; ok {
			leftRsStatuses[repl.Name] = v
		}
	}

	cr.Status.Replsets = leftRsStatuses
	cr.Status.Size = 0
	cr.Status.Ready = 0
	for _, rs := range repls {
		status, err := r.rsStatus(ctx, cr, rs)
		if err != nil {
			return errors.Wrapf(err, "get replset %v status", rs.Name)
		}

		currentRSstatus, ok := cr.Status.Replsets[rs.Name]
		if !ok {
			currentRSstatus = api.ReplsetStatus{}
		}

		status.Members = currentRSstatus.Members
		status.Initialized = currentRSstatus.Initialized
		status.AddedAsShard = currentRSstatus.AddedAsShard

		switch status.Status {
		case api.AppStateReady:
			replsetsReady++
		case api.AppStateStopping:
			replsetsStopping++
		case api.AppStatePaused:
			replsetsPaused++
		}

		if status.Status != currentRSstatus.Status {
			rsCondition := api.ClusterCondition{
				Type:               status.Status,
				Status:             api.ConditionTrue,
				LastTransitionTime: metav1.NewTime(time.Now()),
			}

			switch status.Status {
			case api.AppStateReady:
				rsCondition.Reason = "RSReady"
				rsCondition.Message = rs.Name + ": ready"
			case api.AppStateStopping:
				rsCondition.Reason = "RSStopping"
				rsCondition.Message = rs.Name + ": stopping"
			case api.AppStatePaused:
				rsCondition.Reason = "RSPaused"
				rsCondition.Message = rs.Name + ": paused"
			}

			cr.Status.AddCondition(rsCondition)
		}

		// Ready count can be greater than total size in case of downscale
		if status.Ready > status.Size {
			status.Ready = status.Size
		}

		cr.Status.Replsets[rs.Name] = status
		cr.Status.Size += status.Size
		cr.Status.Ready += status.Ready

		if !inProgress {
			inProgress, err = r.upgradeInProgress(ctx, cr, rs.Name)
			if err != nil {
				return errors.Wrapf(err, "set upgradeInProgres")
			}
		}
	}

	if cr.Spec.Sharding.Enabled {
		mongosStatus, err := r.mongosStatus(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "get mongos status")
		}

		if cr.Status.Mongos == nil {
			cr.Status.Mongos = &api.MongosStatus{}
		}

		if mongosStatus.Status != cr.Status.Mongos.Status {
			mongosCondition := api.ClusterCondition{
				Type:               mongosStatus.Status,
				Status:             api.ConditionTrue,
				LastTransitionTime: metav1.NewTime(time.Now()),
			}

			switch mongosStatus.Status {
			case api.AppStateReady:
				mongosCondition.Reason = "MongosReady"
			case api.AppStateStopping:
				mongosCondition.Reason = "MongosStopping"
			case api.AppStatePaused:
				mongosCondition.Reason = "MongosPaused"
			}

			cr.Status.AddCondition(mongosCondition)
		}

		// Ready count can be greater than total size in case of downscale
		if mongosStatus.Ready > mongosStatus.Size {
			mongosStatus.Ready = mongosStatus.Size
		}

		cr.Status.Mongos = &mongosStatus
		cr.Status.Size += int32(mongosStatus.Size)
		cr.Status.Ready += int32(mongosStatus.Ready)

		if cr.CompareVersion("1.12.0") >= 0 && !inProgress {
			inProgress, err = r.upgradeInProgress(ctx, cr, "mongos")
			if err != nil {
				return errors.Wrapf(err, "set upgradeInProgres")
			}
		}
	} else {
		cr.Status.Mongos = nil
	}

	host, err := r.connectionEndpoint(ctx, cr)
	if err != nil {
		log.Error(err, "get psmdb connection endpoint")
	}
	cr.Status.Host = host

	state := api.AppStateInit

	switch {
	case replsetsStopping > 0 || (cr.Spec.Sharding.Enabled && cr.Status.Mongos.Status == api.AppStateStopping) || cr.ObjectMeta.DeletionTimestamp != nil:
		state = api.AppStateStopping
	case replsetsPaused == len(repls):
		state = api.AppStatePaused
		if cr.Spec.Sharding.Enabled && cr.Status.Mongos.Status != api.AppStatePaused {
			state = api.AppStateStopping
		}
	case !inProgress && replsetsReady == len(repls) && clusterState == api.AppStateReady && cr.Status.Host != "":
		state = api.AppStateReady

		if cr.Spec.Sharding.Enabled && cr.Status.Mongos.Status != api.AppStateReady {
			state = cr.Status.Mongos.Status
		}
	}

	if state != api.AppStateReady {
		log.V(1).Info("Cluster is not ready",
			"upgradeInProgress", inProgress,
			"replsetsReady", replsetsReady,
			"clusterState", clusterState,
		)
	}

	if cr.Status.State != state {
		log.Info("Cluster state changed", "previous", cr.Status.State, "current", state)
	}

	cr.Status.State = state
	clusterCondition.Type = cr.Status.State
	cr.Status.AddCondition(clusterCondition)

	cr.Status.ObservedGeneration = cr.ObjectMeta.Generation

	return r.writeStatus(ctx, cr)
}

func (r *ReconcilePerconaServerMongoDB) upgradeInProgress(ctx context.Context, cr *api.PerconaServerMongoDB, rsName string) (bool, error) {
	sfsObj := &appsv1.StatefulSet{}
	err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name + "-" + rsName, Namespace: cr.Namespace}, sfsObj)
	if err != nil {
		return false, client.IgnoreNotFound(err)
	}

	return sfsObj.Status.Replicas > sfsObj.Status.UpdatedReplicas, nil
}

func (r *ReconcilePerconaServerMongoDB) writeStatus(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		c := &api.PerconaServerMongoDB{}

		err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, c)
		if err != nil {
			return err
		}

		c.Status = cr.Status

		return r.client.Status().Update(ctx, c)
	})

	if k8serrors.IsNotFound(err) {
		return nil
	}

	return errors.Wrap(err, "write status")
}

func (r *ReconcilePerconaServerMongoDB) rsStatus(ctx context.Context, cr *api.PerconaServerMongoDB, rsSpec *api.ReplsetSpec) (api.ReplsetStatus, error) {
	sts := &appsv1.StatefulSet{}
	err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name + "-" + rsSpec.Name, Namespace: cr.Namespace}, sts)
	if err != nil {
		return api.ReplsetStatus{}, client.IgnoreNotFound(err)
	}

	if sts.Annotations[api.AnnotationPVCResizeInProgress] != "" {
		return api.ReplsetStatus{Status: api.AppStateInit}, nil
	}

	list, err := psmdb.GetRSPods(ctx, r.client, cr, rsSpec.Name)
	if err != nil {
		return api.ReplsetStatus{}, fmt.Errorf("get list: %v", err)
	}

	status := api.ReplsetStatus{
		Size:   rsSpec.GetSize(),
		Status: api.AppStateInit,
	}

	for _, pod := range list.Items {
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}

		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			continue
		}

		for _, cntr := range pod.Status.ContainerStatuses {
			if cntr.State.Waiting != nil && cntr.State.Waiting.Message != "" {
				status.Message += cntr.Name + ": " + cntr.State.Waiting.Message + "; "
			}
		}

		for _, cond := range pod.Status.Conditions {
			switch cond.Type {
			case corev1.ContainersReady:
				if cond.Status == corev1.ConditionTrue {
					status.Ready++
				}
			case corev1.PodScheduled:
				if cond.Reason == corev1.PodReasonUnschedulable &&
					cond.LastTransitionTime.Time.Before(time.Now().Add(-1*time.Minute)) {
					status.Status = api.AppStateError
					status.Message = cond.Message
				}
			}
		}
	}

	switch {
	case cr.Spec.Pause && status.Ready > 0:
		status.Status = api.AppStateStopping
	case cr.Spec.Pause:
		status.Status = api.AppStatePaused
	case status.Ready > 0 && status.Size == status.Ready:
		status.Status = api.AppStateReady
	}

	return status, nil
}

func (r *ReconcilePerconaServerMongoDB) mongosStatus(ctx context.Context, cr *api.PerconaServerMongoDB) (api.MongosStatus, error) {
	status := api.MongosStatus{
		Status: api.AppStateInit,
	}

	sts := psmdb.MongosStatefulset(cr)
	err := r.client.Get(ctx, types.NamespacedName{Name: sts.Name, Namespace: sts.Namespace}, sts)
	if err != nil && k8serrors.IsNotFound(err) {
		return status, nil
	}
	if err != nil && !k8serrors.IsNotFound(err) {
		return api.MongosStatus{}, errors.Wrapf(err, "get statefulset %s", sts.Name)
	}

	list, err := r.getMongosPods(ctx, cr)
	if err != nil {
		return api.MongosStatus{}, fmt.Errorf("get list: %v", err)
	}

	status.Size = len(list.Items)

	for _, pod := range list.Items {
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}

		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			continue
		}

		for _, cntr := range pod.Status.ContainerStatuses {
			if cntr.State.Waiting != nil && cntr.State.Waiting.Message != "" {
				status.Message += cntr.Name + ": " + cntr.State.Waiting.Message + "; "
			}
		}

		for _, cond := range pod.Status.Conditions {
			switch cond.Type {
			case corev1.ContainersReady:
				if cond.Status == corev1.ConditionTrue {
					status.Ready++
				}
			case corev1.PodScheduled:
				if cond.Reason == corev1.PodReasonUnschedulable &&
					cond.LastTransitionTime.Time.Before(time.Now().Add(-1*time.Minute)) {
					status.Message = cond.Message
				}
			}
		}
	}

	switch {
	case cr.Spec.Pause && status.Ready > 0:
		status.Status = api.AppStateStopping
	case cr.Spec.Pause:
		status.Status = api.AppStatePaused
	case status.Ready > 0 && status.Size == status.Ready && status.Size == int(cr.Spec.Sharding.Mongos.Size):
		status.Status = api.AppStateReady
	}

	return status, nil
}

func (r *ReconcilePerconaServerMongoDB) connectionEndpoint(ctx context.Context, cr *api.PerconaServerMongoDB) (string, error) {
	if cr.Spec.Sharding.Enabled {
		addrs, err := psmdb.GetMongosAddrs(ctx, r.client, cr, false)
		if err != nil {
			return "", errors.Wrap(err, "get mongos addresses")
		}
		sort.Strings(addrs)
		return strings.Join(addrs, ","), nil
	}

	if rs := cr.Spec.Replsets[0]; rs.Expose.Enabled && (rs.Expose.ExposeType == corev1.ServiceTypeLoadBalancer || rs.Expose.ExposeType == corev1.ServiceTypeClusterIP) {
		list := corev1.PodList{}
		err := r.client.List(ctx,
			&list,
			&client.ListOptions{
				Namespace:     cr.Namespace,
				LabelSelector: labels.SelectorFromSet(naming.RSLabels(cr, rs)),
			},
		)
		if err != nil {
			return "", errors.Wrap(err, "list psmdb pods")
		}
		dnsMode := api.DNSModeInternal
		if rs.Expose.ExposeType == corev1.ServiceTypeLoadBalancer {
			dnsMode = api.DNSModeExternal
		}
		addrs, err := psmdb.GetReplsetAddrs(ctx, r.client, cr, dnsMode, rs, rs.Expose.Enabled, list.Items)
		if err != nil {
			switch errors.Cause(err) {
			case psmdb.ErrNoIngressPoints, psmdb.ErrServiceNotExists:
				return "", nil
			}
			return "", errors.Wrap(err, "get replset addresses")
		}
		sort.Strings(addrs)
		return strings.Join(addrs, ","), nil
	}

	return cr.Name + "-" + cr.Spec.Replsets[0].Name + "." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix, nil
}
