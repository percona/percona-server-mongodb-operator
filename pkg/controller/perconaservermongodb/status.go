package perconaservermongodb

import (
	"context"
	"fmt"
	"strings"
	"time"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *ReconcilePerconaServerMongoDB) updateStatus(cr *api.PerconaServerMongoDB, reconcileErr error, clusterState api.AppState) error {
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

		return r.writeStatus(cr)
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
			cr.Status.Replsets[i].AddedAsShard = nil
		}
	}

	leftRsStatuses := make(map[string]*api.ReplsetStatus)
	for _, repl := range repls {
		if v, ok := cr.Status.Replsets[repl.Name]; ok {
			leftRsStatuses[repl.Name] = v
		}
	}

	cr.Status.Replsets = leftRsStatuses
	cr.Status.Size = 0
	cr.Status.Ready = 0
	for _, rs := range repls {
		status, err := r.rsStatus(cr, rs)
		if err != nil {
			return errors.Wrapf(err, "get replset %v status", rs.Name)
		}

		currentRSstatus, ok := cr.Status.Replsets[rs.Name]
		if !ok {
			currentRSstatus = &api.ReplsetStatus{}
		}

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

		cr.Status.Replsets[rs.Name] = &status
		cr.Status.Size += status.Size
		cr.Status.Ready += status.Ready

		if !inProgress {
			inProgress, err = r.upgradeInProgress(cr, rs.Name)
			if err != nil {
				return errors.Wrapf(err, "set upgradeInProgres")
			}
		}
	}

	if cr.Spec.Sharding.Enabled {
		mongosStatus, err := r.mongosStatus(cr)
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

		cr.Status.Mongos = &mongosStatus
		cr.Status.Size += int32(mongosStatus.Size)
		cr.Status.Ready += int32(mongosStatus.Ready)
	} else {
		cr.Status.Mongos = nil
	}

	// Ready count can be greater than total size in case of downscale
	if cr.Status.Ready > cr.Status.Size {
		cr.Status.Ready = cr.Status.Size
	}

	host, err := r.connectionEndpoint(cr)
	if err != nil {
		log.Error(err, "get psmdb connection endpoint")
	}
	cr.Status.Host = host

	switch {
	case replsetsStopping > 0 || cr.ObjectMeta.DeletionTimestamp != nil:
		cr.Status.State = api.AppStateStopping
	case replsetsPaused == len(repls):
		cr.Status.State = api.AppStatePaused
	case !inProgress && replsetsReady == len(repls) && clusterState == api.AppStateReady && cr.Status.Host != "":
		cr.Status.State = api.AppStateReady
		if cr.Spec.Sharding.Enabled && cr.Status.Mongos.Status != api.AppStateReady {
			cr.Status.State = cr.Status.Mongos.Status
		}
	default:
		cr.Status.State = api.AppStateInit
	}
	clusterCondition.Type = cr.Status.State
	cr.Status.AddCondition(clusterCondition)

	cr.Status.ObservedGeneration = cr.ObjectMeta.Generation

	return r.writeStatus(cr)
}

func (r *ReconcilePerconaServerMongoDB) upgradeInProgress(cr *api.PerconaServerMongoDB, rsName string) (bool, error) {
	sfsObj := &appsv1.StatefulSet{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Name + "-" + rsName, Namespace: cr.Namespace}, sfsObj)
	if err != nil {
		return false, err
	}

	return sfsObj.Status.Replicas > sfsObj.Status.UpdatedReplicas, nil
}

func (r *ReconcilePerconaServerMongoDB) writeStatus(cr *api.PerconaServerMongoDB) error {
	err := r.client.Status().Update(context.TODO(), cr)
	if err != nil {
		// may be it's k8s v1.10 and erlier (e.g. oc3.9) that doesn't support status updates
		// so try to update whole CR
		err := r.client.Update(context.TODO(), cr)
		if err != nil {
			return errors.Wrap(err, "send update")
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) rsStatus(cr *api.PerconaServerMongoDB, rsSpec *api.ReplsetSpec) (api.ReplsetStatus, error) {
	list, err := r.getRSPods(cr, rsSpec.Name)
	if err != nil {
		return api.ReplsetStatus{}, fmt.Errorf("get list: %v", err)
	}

	status := api.ReplsetStatus{
		Size:   rsSpec.Size,
		Status: api.AppStateInit,
	}

	if rsSpec.Arbiter.Enabled {
		status.Size += rsSpec.Arbiter.Size
	}

	for _, pod := range list.Items {
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
	case status.Size == status.Ready:
		status.Status = api.AppStateReady
	}

	return status, nil
}

func (r *ReconcilePerconaServerMongoDB) mongosStatus(cr *api.PerconaServerMongoDB) (api.MongosStatus, error) {
	list, err := r.getMongosPods(cr)
	if err != nil {
		return api.MongosStatus{}, fmt.Errorf("get list: %v", err)
	}

	status := api.MongosStatus{
		Size:   len(list.Items),
		Status: api.AppStateInit,
	}

	for _, pod := range list.Items {
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
	case status.Size == status.Ready:
		status.Status = api.AppStateReady
	}

	return status, nil
}

func (r *ReconcilePerconaServerMongoDB) connectionEndpoint(cr *api.PerconaServerMongoDB) (string, error) {
	if cr.Spec.Sharding.Enabled {
		if mongos := cr.Spec.Sharding.Mongos; mongos.Expose.ExposeType == corev1.ServiceTypeLoadBalancer {
			return loadBalancerServiceEndpoint(r.client, cr.Name+"-mongos", cr.Namespace)
		}
		return cr.Name + "-mongos." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix, nil
	}

	if rs := cr.Spec.Replsets[0]; rs.Expose.Enabled &&
		rs.Expose.ExposeType == corev1.ServiceTypeLoadBalancer {
		list := corev1.PodList{}
		err := r.client.List(context.TODO(),
			&list,
			&client.ListOptions{
				Namespace: cr.Namespace,
				LabelSelector: labels.SelectorFromSet(map[string]string{
					"app.kubernetes.io/name":       "percona-server-mongodb",
					"app.kubernetes.io/instance":   cr.Name,
					"app.kubernetes.io/replset":    rs.Name,
					"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
					"app.kubernetes.io/part-of":    "percona-server-mongodb",
				}),
			},
		)
		if err != nil {
			return "", errors.Wrap(err, "list psmdb pods")
		}
		addrs, err := psmdb.GetReplsetAddrs(r.client, cr, rs.Name, rs.Expose.Enabled, list.Items)
		if err != nil {
			return "", err
		}
		return strings.Join(addrs, ","), nil
	}

	return cr.Name + "-" + cr.Spec.Replsets[0].Name + "." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix, nil
}

func loadBalancerServiceEndpoint(client client.Client, serviceName, namespace string) (string, error) {
	host := ""
	srv := corev1.Service{}
	err := client.Get(context.TODO(), types.NamespacedName{
		Namespace: namespace,
		Name:      serviceName,
	}, &srv)
	if err != nil {
		return "", errors.Wrap(err, "get service")
	}
	for _, i := range srv.Status.LoadBalancer.Ingress {
		host = i.IP
		if len(i.Hostname) > 0 {
			host = i.Hostname
		}
	}
	return host, nil
}
