package perconaservermongodb

import (
	"context"
	"fmt"
	dlog "log"
	"sort"
	"strings"
	"time"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *ReconcilePerconaServerMongoDB) smartUpdate(cr *api.PerconaServerMongoDB, sfs *appsv1.StatefulSet, replset *api.ReplsetSpec, secret *corev1.Secret) error {
	if cr.Spec.UpdateStrategy != api.SmartUpdateStatefulSetStrategyType {
		return nil
	}

	if sfs.Status.UpdatedReplicas >= sfs.Status.Replicas {
		return nil
	}

	log.Info("statefullSet was changed, run smart update")

	// TODO: check backup running

	if sfs.Status.ReadyReplicas < sfs.Status.Replicas {
		return fmt.Errorf("can't start/continue 'SmartUpdate': waiting for all replicas are ready")
	}

	list := corev1.PodList{}
	if err := r.client.List(context.TODO(),
		&client.ListOptions{
			Namespace: cr.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"app.kubernetes.io/name":       "percona-server-mongodb",
				"app.kubernetes.io/instance":   cr.Name,
				"app.kubernetes.io/replset":    replset.Name,
				"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
				"app.kubernetes.io/part-of":    "percona-server-mongodb",
			}),
		},
		&list,
	); err != nil {
		return fmt.Errorf("get pod list: %v", err)
	}

	primary, err := r.getPrimaryPod(cr, replset, list, secret)
	if err != nil {
		return fmt.Errorf("get primary pod: %v", err)
	}

	log.Info(fmt.Sprintf("primary pod is %s", primary))

	waitLimit := int(replset.LivenessProbe.InitialDelaySeconds)

	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].Name > list.Items[j].Name
	})

	var primaryPod corev1.Pod
	for _, pod := range list.Items {
		pod := pod
		if strings.HasPrefix(primary, fmt.Sprintf("%s.%s.%s", pod.Name, sfs.Name, sfs.Namespace)) {
			primaryPod = pod
		} else {
			log.Info(fmt.Sprintf("apply changes to secondary pod %s", pod.Name))
			if err := r.applyNWait(cr, sfs.Status.UpdateRevision, &pod, waitLimit); err != nil {
				return fmt.Errorf("failed to apply changes: %v", err)
			}
		}
	}

	log.Info(fmt.Sprintf("apply changes to primary pod %s", primaryPod.Name))
	if err := r.applyNWait(cr, sfs.Status.UpdateRevision, &primaryPod, waitLimit); err != nil {
		return fmt.Errorf("failed to apply changes: %v", err)
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) applyNWait(cr *api.PerconaServerMongoDB, updateRevision string, pod *corev1.Pod, waitLimit int) error {
	if pod.ObjectMeta.Labels["controller-revision-hash"] == updateRevision {
		log.Info(fmt.Sprintf("pod %s is already updated", pod.Name))
	} else {
		if err := r.client.Delete(context.TODO(), pod); err != nil {
			return fmt.Errorf("failed to delete pod: %v", err)
		}
	}

	if err := r.waitPodRestart(updateRevision, pod, waitLimit); err != nil {
		return fmt.Errorf("failed to wait pod: %v", err)
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) waitPodRestart(updateRevision string, pod *corev1.Pod, waitLimit int) error {
	for i := 0; i < waitLimit; i++ {
		time.Sleep(time.Second * 1)

		err := r.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, pod)
		if err != nil && !k8sErrors.IsNotFound(err) {
			return err
		}

		ready := false
		for _, container := range pod.Status.ContainerStatuses {
			if container.Name == "mongod" {
				ready = container.Ready
			}
		}

		if pod.Status.Phase == corev1.PodRunning && pod.ObjectMeta.Labels["controller-revision-hash"] == updateRevision && ready {
			log.Info(fmt.Sprintf("pod %s started", pod.Name))
			return nil
		}
	}

	return fmt.Errorf("reach pod wait limit")
}

func (r *ReconcilePerconaServerMongoDB) getPrimaryPod(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pods corev1.PodList, usersSecret *corev1.Secret) (string, error) {
	rsAddrs, err := psmdb.GetReplsetAddrs(r.client, cr, replset, pods.Items)
	if err != nil {
		return "", errors.Wrap(err, "failed to get replset addr")
	}

	dlog.Printf("DEBUG: get primary pod %v", rsAddrs)

	client, err := mongo.Dial(rsAddrs, replset.Name, usersSecret, false)
	if err != nil {
		return "", errors.Wrap(err, "failed to dial mongo")
	}

	status, err := mongo.RSStatus(context.TODO(), client)
	if err != nil {
		return "", errors.Wrap(err, "failed to get rs status")
	}

	return status.Primary().Name, nil
}
