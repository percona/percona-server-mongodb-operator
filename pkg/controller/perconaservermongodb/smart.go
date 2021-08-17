package perconaservermongodb

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	mgo "go.mongodb.org/mongo-driver/mongo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *ReconcilePerconaServerMongoDB) smartUpdate(cr *api.PerconaServerMongoDB, sfs *appsv1.StatefulSet,
	replset *api.ReplsetSpec) error {

	if replset.Size == 0 {
		return nil
	}

	if cr.Spec.UpdateStrategy != api.SmartUpdateStatefulSetStrategyType {
		return nil
	}

	if sfs.Status.UpdatedReplicas >= sfs.Status.Replicas {
		return nil
	}

	if cr.CompareVersion("1.4.0") < 0 {
		return nil
	}

	if cr.Spec.Sharding.Enabled && sfs.Name != cr.Name+"-"+api.ConfigReplSetName {
		cfgSfs := appsv1.StatefulSet{}
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Name + "-" + api.ConfigReplSetName, Namespace: cr.Namespace}, &cfgSfs)
		if err != nil {
			return errors.Wrapf(err, "get config statefulset %s/%s", cr.Namespace, cr.Name+"-"+api.ConfigReplSetName)
		}

		if cfgSfs.Status.UpdatedReplicas < cfgSfs.Status.Replicas {
			log.Info("waiting for config RS update")
			return nil
		}
	}

	log.Info("statefullSet was changed, start smart update", "name", sfs.Name)

	if sfs.Status.ReadyReplicas < sfs.Status.Replicas {
		log.Info("can't start/continue 'SmartUpdate': waiting for all replicas are ready")
		return nil
	}

	isBackupRunning, err := r.isBackupRunning(cr)
	if err != nil {
		return errors.Wrap(err, "failed to check active backups")
	}
	if isBackupRunning {
		log.Info("can't start 'SmartUpdate': waiting for running backups to be finished")
		return nil
	}

	hasActiveJobs, err := backup.HasActiveJobs(r.client, cr, backup.Job{}, backup.NotPITRLock)
	if err != nil {
		return errors.Wrap(err, "failed to check active jobs")
	}

	if hasActiveJobs {
		log.Info("can't start 'SmartUpdate': waiting for active jobs to be finished")
		return nil
	}

	if sfs.Name == cr.Name+"-"+api.ConfigReplSetName {
		err = r.disableBalancer(cr)
		if err != nil {
			return errors.Wrap(err, "failed to stop balancer")
		}
	}

	list := corev1.PodList{}
	if err := r.client.List(context.TODO(),
		&list,
		&k8sclient.ListOptions{
			Namespace: cr.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"app.kubernetes.io/name":       "percona-server-mongodb",
				"app.kubernetes.io/instance":   cr.Name,
				"app.kubernetes.io/replset":    replset.Name,
				"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
				"app.kubernetes.io/part-of":    "percona-server-mongodb",
			}),
		},
	); err != nil {
		return fmt.Errorf("get pod list: %v", err)
	}

	client, err := r.mongoClientWithRole(cr, *replset, roleClusterAdmin)
	if err != nil {
		return fmt.Errorf("failed to get mongo client: %v", err)
	}

	defer func() {
		err := client.Disconnect(context.TODO())
		if err != nil {
			log.Error(err, "failed to close connection")
		}
	}()

	primary, err := r.getPrimaryPod(client)
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
		if replset.Expose.Enabled {
			host, err := psmdb.MongoHost(r.client, cr, replset.Name, replset.Expose.Enabled, pod)
			if err != nil {
				return errors.Wrapf(err, "get mongo host for pod %s", pod.Name)
			}

			if host == primary {
				primaryPod = pod
				continue
			}
		}

		if strings.HasPrefix(primary, fmt.Sprintf("%s.%s.%s", pod.Name, sfs.Name, sfs.Namespace)) {
			primaryPod = pod
			continue
		}

		log.Info(fmt.Sprintf("apply changes to secondary pod %s", pod.Name))

		updateRevision := sfs.Status.UpdateRevision

		if pod.Labels["app.kubernetes.io/component"] == "arbiter" {
			arbiterSfs, err := r.getArbiterStatefulset(cr, pod.Labels["app.kubernetes.io/replset"])
			if err != nil {
				return errors.Wrap(err, "failed to get arbiter statefilset")
			}

			updateRevision = arbiterSfs.Status.UpdateRevision
		}

		if err := r.applyNWait(cr, updateRevision, &pod, waitLimit); err != nil {
			return errors.Wrap(err, "failed to apply changes")
		}
	}

	forceStepDown := replset.Size == 1
	log.Info("doing step down...", "force", forceStepDown)
	err = mongo.StepDown(context.TODO(), client, forceStepDown)
	if err != nil {
		return errors.Wrap(err, "failed to do step down")
	}

	log.Info(fmt.Sprintf("apply changes to primary pod %s", primaryPod.Name))
	if err := r.applyNWait(cr, sfs.Status.UpdateRevision, &primaryPod, waitLimit); err != nil {
		return fmt.Errorf("failed to apply changes: %v", err)
	}

	log.Info("smart update finished for statefulset", "statefulset", sfs.Name)

	return nil
}

func (r *ReconcilePerconaServerMongoDB) isAllSfsUpToDate(cr *api.PerconaServerMongoDB) (bool, error) {
	sfsList := appsv1.StatefulSetList{}
	if err := r.client.List(context.TODO(), &sfsList,
		&k8sclient.ListOptions{
			Namespace: cr.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"app.kubernetes.io/instance": cr.Name,
			}),
		},
	); err != nil {
		return false, errors.Wrap(err, "failed to get statefulset list")
	}

	for _, s := range sfsList.Items {
		if s.Status.UpdatedReplicas < s.Status.Replicas {
			return false, nil
		}
	}

	return true, nil
}

func (r *ReconcilePerconaServerMongoDB) applyNWait(cr *api.PerconaServerMongoDB, updateRevision string, pod *corev1.Pod, waitLimit int) error {
	if pod.ObjectMeta.Labels["controller-revision-hash"] == updateRevision {
		log.Info(fmt.Sprintf("pod %s is already updated", pod.Name))
	} else {
		if err := r.client.Delete(context.TODO(), pod); err != nil {
			return errors.Wrap(err, "delete pod")
		}
	}

	if err := r.waitPodRestart(cr, updateRevision, pod, waitLimit); err != nil {
		return errors.Wrap(err, "wait pod restart")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) waitPodRestart(cr *api.PerconaServerMongoDB, updateRevision string, pod *corev1.Pod, waitLimit int) error {
	for i := 0; i < waitLimit; i++ {
		time.Sleep(time.Second * 1)

		err := r.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, pod)
		if err != nil && !k8sErrors.IsNotFound(err) {
			return errors.Wrap(err, "get pod")
		}

		// We update status in every loop to not wait until the end of smart update
		if err := r.updateStatus(cr, nil, api.AppStateInit); err != nil {
			return errors.Wrap(err, "update status")
		}

		ready := false
		for _, container := range pod.Status.ContainerStatuses {
			if container.Name == "mongod" || container.Name == "mongod-arbiter" {
				ready = container.Ready
			}
		}

		if pod.Status.Phase == corev1.PodRunning && pod.ObjectMeta.Labels["controller-revision-hash"] == updateRevision && ready {
			log.Info(fmt.Sprintf("pod %s started", pod.Name))
			return nil
		}
	}

	return errors.New("reach pod wait limit")
}

func (r *ReconcilePerconaServerMongoDB) getPrimaryPod(client *mgo.Client) (string, error) {
	status, err := mongo.RSStatus(context.TODO(), client)
	if err != nil {
		return "", errors.Wrap(err, "failed to get rs status")
	}

	return status.Primary().Name, nil
}
