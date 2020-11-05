package perconaservermongodb

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	mgo "go.mongodb.org/mongo-driver/mongo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *ReconcilePerconaServerMongoDB) smartUpdate(cr *api.PerconaServerMongoDB, sfs *appsv1.StatefulSet,
	replset *api.ReplsetSpec, secret *corev1.Secret) error {

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

	ok, err := r.isBackupRunning(cr)
	if err != nil {
		return fmt.Errorf("failed to check active backups: %v", err)
	}
	if ok {
		log.Info("can't start 'SmartUpdate': waiting for running backups finished")
		return nil
	}

	username := string(secret.Data[envMongoDBClusterAdminUser])
	password := string(secret.Data[envMongoDBClusterAdminPassword])

	if sfs.Name == cr.Name+"-"+api.ConfigReplSetName {
		err := stopBalancerIfNeeded(cr, username, password)
		if err != nil {
			return errors.Wrap(err, "failed to stop balancer")
		}
	}

	list := corev1.PodList{}
	if err := r.client.List(context.TODO(),
		&list,
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
	); err != nil {
		return fmt.Errorf("get pod list: %v", err)
	}

	client, err := r.mongoClient(cr, replset, list, username, password)
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

	log.Info("doing step down...")
	err = mongo.StepDown(context.TODO(), client)
	if err != nil {
		return errors.Wrap(err, "failed to do step down")
	}

	log.Info(fmt.Sprintf("apply changes to primary pod %s", primaryPod.Name))
	if err := r.applyNWait(cr, sfs.Status.UpdateRevision, &primaryPod, waitLimit); err != nil {
		return fmt.Errorf("failed to apply changes: %v", err)
	}

	err = r.startBalancerIfNeeded(cr, username, password)
	if err != nil {
		return errors.Wrap(err, "failed to start balancer")
	}

	log.Info("smart update finished")

	return nil
}

func stopBalancerIfNeeded(cr *api.PerconaServerMongoDB, username, password string) error {
	if !cr.Spec.Sharding.Enabled {
		return nil
	}

	mongosSession, err := mongosConn(cr, username, password)
	if err != nil {
		return errors.Wrap(err, "failed to get mongos connection")
	}

	defer func() {
		err := mongosSession.Disconnect(context.TODO())
		if err != nil {
			log.Error(err, "failed to close mongos connection")
		}
	}()

	err = mongo.SwitchBalancer(context.TODO(), mongosSession, false)
	if err != nil {
		return errors.Wrap(err, "failed to stop balancer")
	}

	log.Info("balancer stopped")

	return nil
}

func (r *ReconcilePerconaServerMongoDB) isAllSfsUpToDate(cr *api.PerconaServerMongoDB) (bool, error) {
	sfsList := appsv1.StatefulSetList{}
	if err := r.client.List(context.TODO(), &sfsList,
		&client.ListOptions{
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

func (r *ReconcilePerconaServerMongoDB) startBalancerIfNeeded(cr *api.PerconaServerMongoDB, username, password string) error {
	if !cr.Spec.Sharding.Enabled {
		return nil
	}

	uptodate, err := r.isAllSfsUpToDate(cr)
	if err != nil {
		return errors.Wrap(err, "failed to chaeck if all sfs are up to date")
	}

	if !uptodate {
		return nil
	}

	mongosSession, err := mongosConn(cr, username, password)
	if err != nil {
		return errors.Wrap(err, "failed to get mongos connection")
	}

	defer func() {
		err := mongosSession.Disconnect(context.TODO())
		if err != nil {
			log.Error(err, "failed to close mongos connection")
		}
	}()

	run, err := mongo.IsBalancerRunning(context.TODO(), mongosSession)
	if err != nil {
		return errors.Wrap(err, "failed to check if balancer running")
	}

	if !run {
		err := mongo.SwitchBalancer(context.TODO(), mongosSession, true)
		if err != nil {
			return errors.Wrap(err, "failed to start balancer")
		}
	}

	log.Info("balancer started")

	return nil
}

func mongosConn(cr *api.PerconaServerMongoDB, username, password string) (*mgo.Client, error) {
	conf := mongo.Config{
		Hosts: []string{strings.Join([]string{cr.Name + "-mongos", cr.Namespace, cr.Spec.ClusterServiceDNSSuffix}, ".") +
			":" + strconv.Itoa(int(cr.Spec.Sharding.Mongos.Port))},
		Username: username,
		Password: password,
	}

	return mongo.Dial(&conf)
}

func (r *ReconcilePerconaServerMongoDB) isBackupRunning(cr *api.PerconaServerMongoDB) (bool, error) {
	bcps := api.PerconaServerMongoDBBackupList{}
	if err := r.client.List(context.TODO(), &bcps, &client.ListOptions{Namespace: cr.Namespace}); err != nil {
		if k8sErrors.IsNotFound(err) {
			return false, nil
		}
		return false, errors.Wrap(err, "get backup list")
	}

	for _, bcp := range bcps.Items {
		if bcp.Status.State != api.BackupStateReady &&
			bcp.Status.State != api.BackupStateError {
			return true, nil
		}
	}

	return false, nil
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

func (r *ReconcilePerconaServerMongoDB) getPrimaryPod(client *mgo.Client) (string, error) {
	status, err := mongo.RSStatus(context.TODO(), client)
	if err != nil {
		return "", errors.Wrap(err, "failed to get rs status")
	}

	return status.Primary().Name, nil
}
