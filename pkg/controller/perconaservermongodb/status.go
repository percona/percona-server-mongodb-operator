package perconaservermongodb

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const maxStatusesQuantity = 20

func (r *ReconcilePerconaServerMongoDB) updateStatus(cr *api.PerconaServerMongoDB, reconcileErr error) error {
	clusterCondition := api.ClusterCondition{
		Status:             api.ConditionTrue,
		Type:               api.ClusterInit,
		LastTransitionTime: metav1.NewTime(time.Now()),
	}
	if reconcileErr != nil {
		if cr.Status.Status != api.ClusterError {
			clusterCondition = api.ClusterCondition{
				Status:             api.ConditionTrue,
				Type:               api.ClusterError,
				Message:            reconcileErr.Error(),
				Reason:             "ErrorReconcile",
				LastTransitionTime: metav1.NewTime(time.Now()),
			}
			cr.Status.Conditions = append(cr.Status.Conditions, clusterCondition)
		}

		cr.Status.Message = "Error: " + reconcileErr.Error()
		cr.Status.Status = api.ClusterError

		return r.writeStatus(cr)
	}

	cr.Status.Message = ""

	replsetsReady := 0
	inProgress := false
	for _, rs := range cr.Spec.Replsets {
		status, err := r.rsStatus(rs, cr.Name, cr.Namespace)
		if err != nil {
			return errors.Wrapf(err, "get replset %v status", rs.Name)
		}

		currentRSstatus, ok := cr.Status.Replsets[rs.Name]
		if !ok {
			currentRSstatus = &api.ReplsetStatus{}
		}

		status.Initialized = currentRSstatus.Initialized

		if status.Status == api.AppStateReady {
			replsetsReady++
		}

		if status.Status == api.AppStateReady && currentRSstatus.Initialized {
			clusterCondition = api.ClusterCondition{
				Status:             api.ConditionTrue,
				Type:               api.ClusterRSReady,
				LastTransitionTime: metav1.NewTime(time.Now()),
			}
		}

		if status.Status == api.AppStateError {
			clusterCondition = api.ClusterCondition{
				Status:             api.ConditionTrue,
				Message:            rs.Name + ": " + status.Message,
				Reason:             "ErrorRS",
				Type:               api.ClusterError,
				LastTransitionTime: metav1.NewTime(time.Now()),
			}
		}

		cr.Status.Replsets[rs.Name] = &status
		if !inProgress {
			inProgress, err = r.upgradeInProgress(cr, rs.Name)
			if err != nil {
				return errors.Wrapf(err, "set upgradeInProgres")
			}
		}
	}

	if len(cr.Status.Conditions) == 0 || cr.Status.Conditions[0].Type == api.ClusterError {
		cr.Status.Conditions = append(cr.Status.Conditions, clusterCondition)
	} else {
		lastClusterCondition := cr.Status.Conditions[len(cr.Status.Conditions)-1]
		switch {
		case lastClusterCondition.Type != clusterCondition.Type:
			cr.Status.Conditions = append(cr.Status.Conditions, clusterCondition)
		default:
			cr.Status.Conditions[len(cr.Status.Conditions)-1] = lastClusterCondition
		}
	}

	if len(cr.Status.Conditions) > maxStatusesQuantity {
		cr.Status.Conditions = cr.Status.Conditions[len(cr.Status.Conditions)-maxStatusesQuantity:]
	}

	cr.Status.Status = api.AppStateInit
	if replsetsReady == len(cr.Spec.Replsets) {
		cr.Status.Status = api.AppStateReady
	}

	if inProgress {
		cr.Status.Status = api.AppStateInit
	}

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

func (r *ReconcilePerconaServerMongoDB) rsStatus(rsSpec *api.ReplsetSpec, clusterName, namespace string) (api.ReplsetStatus, error) {
	list := corev1.PodList{}
	err := r.client.List(context.TODO(),
		&client.ListOptions{
			Namespace: namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"app.kubernetes.io/name":       "percona-server-mongodb",
				"app.kubernetes.io/instance":   clusterName,
				"app.kubernetes.io/replset":    rsSpec.Name,
				"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
				"app.kubernetes.io/part-of":    "percona-server-mongodb",
			}),
		},
		&list,
	)
	if err != nil {
		return api.ReplsetStatus{}, fmt.Errorf("get list: %v", err)
	}

	status := api.ReplsetStatus{
		Size:   rsSpec.Size,
		Status: api.AppStateInit,
	}

	for _, pod := range list.Items {
		for _, cond := range pod.Status.Conditions {
			switch cond.Type {
			case corev1.ContainersReady:
				if cond.Status == corev1.ConditionTrue {
					status.Ready++
				} else if cond.Status == corev1.ConditionFalse {
					for _, cntr := range pod.Status.ContainerStatuses {
						if cntr.State.Waiting != nil && cntr.State.Waiting.Message != "" {
							status.Message += cntr.Name + ": " + cntr.State.Waiting.Message + "; "
						}
					}
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

	if status.Size == status.Ready {
		status.Status = api.AppStateReady
	}

	return status, nil
}
