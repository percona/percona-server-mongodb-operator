package perconaservermongodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/pmm"
)

func getInternalSecretData(cr *api.PerconaServerMongoDB, secret *corev1.Secret) map[string][]byte {
	m := secret.DeepCopy().Data
	if cr.CompareVersion("1.19.0") >= 0 {
		for k, v := range secret.Data {
			m[k+"_ESCAPED"] = []byte(url.QueryEscape(string(v)))
		}
	}
	return m
}

func (r *ReconcilePerconaServerMongoDB) reconcileUsers(ctx context.Context, cr *api.PerconaServerMongoDB, repls []*api.ReplsetSpec) error {
	sysUsersSecretObj := corev1.Secret{}
	err := r.client.Get(
		ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      cr.Spec.Secrets.Users,
		},
		&sysUsersSecretObj,
	)
	if err != nil && k8serrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return errors.Wrapf(err, "get sys users secret '%s'", cr.Spec.Secrets.Users)
	}

	secretName := api.InternalUserSecretName(cr)
	internalSysSecretObj := corev1.Secret{}

	err = r.client.Get(
		ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      secretName,
		},
		&internalSysSecretObj,
	)
	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "get internal sys users secret")
	}

	if k8serrors.IsNotFound(err) {
		internalSysUsersSecret := sysUsersSecretObj.DeepCopy()
		internalSysUsersSecret.ObjectMeta = metav1.ObjectMeta{
			Name:      secretName,
			Namespace: cr.Namespace,
			Labels:    naming.ClusterLabels(cr),
		}
		if cr.CompareVersion("1.17.0") < 0 {
			internalSysUsersSecret.Labels = nil
		}
		if cr.CompareVersion("1.19.0") >= 0 {
			internalSysUsersSecret.Data = getInternalSecretData(cr, &sysUsersSecretObj)
		}
		err = r.client.Create(ctx, internalSysUsersSecret)
		if err != nil {
			return errors.Wrap(err, "create internal sys users secret")
		}
		return nil
	}

	// we do this check after work with secret objects because in case of upgrade cluster we need to be sure that internal secret exist
	if cr.Status.State != api.AppStateReady {
		return nil
	}

	cred, err := getCredentials(&sysUsersSecretObj, api.RoleDatabaseAdmin)
	if err != nil {
		return errors.Wrap(err, "get database admin credentials")
	}
	if err := ensureConnectionStringSecret(
		ctx,
		r.client,
		cr,
		naming.SecretDatabaseAdminConnStrName(cr),
		string(api.RoleDatabaseAdmin),
		cred,
		true,
	); err != nil {
		return errors.Wrap(err, "ensure connection string secret")
	}

	dataChanged, err := sysUsersSecretDataChanged(cr, &sysUsersSecretObj, &internalSysSecretObj)
	if err != nil {
		return errors.Wrap(err, "check sys users data changes")
	}

	if cr.Spec.Unmanaged {
		return nil
	}

	pending := pendingRestarts(&internalSysSecretObj)
	if !dataChanged && len(pending) == 0 {
		return nil
	}

	if dataChanged {
		logf.FromContext(ctx).Info("Secret data changed. Updating users...")

		containerNames, err := r.updateSysUsers(ctx, cr, &sysUsersSecretObj, &internalSysSecretObj, repls)
		if err != nil {
			return errors.Wrap(err, "manage sys users")
		}

		// MongoDB has the new credentials now. Store them, and the containers that
		// still have to restart, before restarting any: the operator connects with
		// the internal secret, and with old credentials in it after a failed restart
		// it could not log in again. Restarted containers read their credentials
		// from this secret too.
		internalSysSecretObj.Data = sysUsersSecretObj.Data
		if cr.CompareVersion("1.19.0") >= 0 {
			internalSysSecretObj.Data = getInternalSecretData(cr, &sysUsersSecretObj)
		}
		pending = mergeRestarts(pending, containerNames)
		setPendingRestarts(&internalSysSecretObj, pending)
		if err := r.client.Update(ctx, &internalSysSecretObj); err != nil {
			return errors.Wrap(err, "update internal sys users secret")
		}
	}

	if len(pending) == 0 {
		return nil
	}

	pods, err := r.podsWithSystemUsers(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "get pods")
	}

	var errs []error
	for _, name := range pending {
		if err := r.killContainer(ctx, pods, name); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		// The pending restarts stay in the internal secret, the next reconcile retries them.
		return errors.Wrap(stderrors.Join(errs...), "restart containers")
	}

	setPendingRestarts(&internalSysSecretObj, nil)
	return errors.Wrap(r.client.Update(ctx, &internalSysSecretObj), "clear pending restarts")
}

func (r *ReconcilePerconaServerMongoDB) podsWithSystemUsers(ctx context.Context, cr *api.PerconaServerMongoDB) ([]corev1.Pod, error) {
	rsPodList, err := r.getMongodPods(ctx, cr)
	if err != nil {
		return nil, errors.Wrap(err, "get mongod pods")
	}
	pods := rsPodList.Items

	if cr.Spec.Sharding.Enabled {
		mongosList, err := r.getMongosPods(ctx, cr)
		if err != nil {
			return nil, errors.Wrap(err, "get mongos pods")
		}
		pods = append(pods, mongosList.Items...)

		cfgPodlist, err := psmdb.GetRSPods(ctx, r.client, cr, api.ConfigReplSetName)
		if err != nil {
			return nil, errors.Wrap(err, "get cfg pods")
		}
		pods = append(pods, cfgPodlist.Items...)
	}

	return pods, nil
}

func pendingRestarts(secret *corev1.Secret) []string {
	v := secret.Annotations[naming.AnnotationPendingRestarts]
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

func setPendingRestarts(secret *corev1.Secret, containers []string) {
	if len(containers) == 0 {
		delete(secret.Annotations, naming.AnnotationPendingRestarts)
		return
	}
	if secret.Annotations == nil {
		secret.Annotations = map[string]string{}
	}
	secret.Annotations[naming.AnnotationPendingRestarts] = strings.Join(containers, ",")
}

func mergeRestarts(pending, add []string) []string {
	for _, name := range add {
		if !slices.Contains(pending, name) {
			pending = append(pending, name)
		}
	}
	return pending
}

// containerRestartChecks is how many times killContainer reads a pod to see
// whether a container restarted although the exec that killed it failed.
const containerRestartChecks = 5

func (r *ReconcilePerconaServerMongoDB) killContainer(ctx context.Context, pods []corev1.Pod, containerName string) error {
	log := logf.FromContext(ctx)

	var errs []error
	for i := range pods {
		pod := &pods[i]
		if getPodContainer(pod, containerName) == nil {
			continue
		}

		log.Info("Restarting container", "pod", pod.Name, "container", containerName)
		before := containerStatus(pod, containerName)

		err := r.execKill(ctx, pod, containerName)
		// "kill 1" ends the exec session together with the container, so the
		// exec can fail although the container was restarted.
		if err != nil && r.containerRestarted(ctx, pod, containerName, before) {
			log.Info("Container restarted, ignoring the exec error", "pod", pod.Name, "container", containerName, "error", err.Error())
			err = nil
		}
		if err != nil {
			err = retry.OnError(retry.DefaultBackoff, func(_ error) bool { return true }, func() error {
				return r.execKill(ctx, pod, containerName)
			})
		}
		if err != nil {
			errs = append(errs, errors.Wrapf(err, "restart %s in pod %s", containerName, pod.Name))
		}
	}

	return stderrors.Join(errs...)
}

func (r *ReconcilePerconaServerMongoDB) execKill(ctx context.Context, pod *corev1.Pod, containerName string) error {
	stderrBuf := &bytes.Buffer{}

	err := r.clientcmd.Exec(ctx, pod, containerName, []string{"/bin/sh", "-c", "kill 1"}, nil, nil, stderrBuf, false)
	if err != nil {
		return errors.Wrap(err, "exec command in pod")
	}

	if stderrBuf.Len() != 0 {
		return errors.Errorf("exec command return error: %s", stderrBuf.String())
	}

	return nil
}

// containerRestarted reports whether the container was replaced since before
// was taken. The kubelet reports a restart with a delay, so it checks the pod
// a few times.
func (r *ReconcilePerconaServerMongoDB) containerRestarted(ctx context.Context, pod *corev1.Pod, containerName string, before *corev1.ContainerStatus) bool {
	if before == nil {
		return false
	}

	for range containerRestartChecks {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(r.podPollInterval()):
		}

		current := new(corev1.Pod)
		if err := r.client.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, current); err != nil {
			logf.FromContext(ctx).V(1).Info("Get pod", "pod", pod.Name, "error", err.Error())
			continue
		}

		now := containerStatus(current, containerName)
		if now != nil && (now.RestartCount != before.RestartCount || now.ContainerID != before.ContainerID) {
			return true
		}
	}

	return false
}

func containerStatus(pod *corev1.Pod, name string) *corev1.ContainerStatus {
	for i := range pod.Status.ContainerStatuses {
		if pod.Status.ContainerStatuses[i].Name == name {
			return &pod.Status.ContainerStatuses[i]
		}
	}
	return nil
}

type systemUser struct {
	currName []byte
	name     []byte
	pass     []byte
}

type systemUsers struct {
	currData map[string][]byte // data stored in internal secret
	newData  map[string][]byte // data stored in users secret
	users    []systemUser
}

// add appends user to su.users by given keys if user should be changed
func (su *systemUsers) add(nameKey, passKey string) (changed bool, err error) {
	if len(su.newData[nameKey]) == 0 {
		return false, errors.New("undefined or not exist user name " + nameKey)
	}
	if len(su.newData[passKey]) == 0 {
		return false, errors.New("undefined or not exist user pass " + nameKey)
	}

	// no changes, nothing to do with that user
	if bytes.Equal(su.newData[nameKey], su.currData[nameKey]) &&
		bytes.Equal(su.newData[passKey], su.currData[passKey]) {
		return false, nil
	}
	if passKey == api.EnvPMMServerToken {
		return true, nil
	}
	su.users = append(su.users, systemUser{
		currName: su.currData[nameKey],
		name:     su.newData[nameKey],
		pass:     su.newData[passKey],
	})

	return true, nil
}

func (su *systemUsers) len() int {
	return len(su.users)
}

func (r *ReconcilePerconaServerMongoDB) updateSysUsers(ctx context.Context, cr *api.PerconaServerMongoDB, newUsersSec, currUsersSec *corev1.Secret,
	repls []*api.ReplsetSpec,
) ([]string, error) {
	su := systemUsers{
		currData: currUsersSec.Data,
		newData:  newUsersSec.Data,
	}

	containers := []string{}

	type user struct {
		nameKey, passKey string
	}
	users := []user{
		{
			nameKey: api.EnvMongoDBClusterAdminUser,
			passKey: api.EnvMongoDBClusterAdminPassword,
		},

		{
			nameKey: api.EnvMongoDBClusterMonitorUser,
			passKey: api.EnvMongoDBClusterMonitorPassword,
		},

		{
			nameKey: api.EnvMongoDBBackupUser,
			passKey: api.EnvMongoDBBackupPassword,
		},

		// !!! UserAdmin always must be the last to update since we're using it for the mongo connection
		{
			nameKey: api.EnvMongoDBUserAdminUser,
			passKey: api.EnvMongoDBUserAdminPassword,
		},
	}
	if _, ok := currUsersSec.Data[api.EnvMongoDBDatabaseAdminUser]; cr.CompareVersion("1.13.0") >= 0 && ok {
		users = append([]user{
			{
				nameKey: api.EnvMongoDBDatabaseAdminUser,
				passKey: api.EnvMongoDBDatabaseAdminPassword,
			},
		}, users...)
	}
	// Only enqueue the search user for password rotation if it already
	// exists in the internal secret. On first enable the user is
	// provisioned by createOrUpdateSystemUsers; queueing it here would
	// try UpdateUser against a non-existent account.
	if _, ok := currUsersSec.Data[api.EnvMongoDBSearchUser]; ok && cr.IsSearchEnabled() {
		users = append(users, user{
			nameKey: api.EnvMongoDBSearchUser,
			passKey: api.EnvMongoDBSearchPassword,
		})
	}
	if cr.Spec.PMM.Enabled {
		if pmm.SecretHasToken(newUsersSec) {
			users = append([]user{
				{
					nameKey: api.PMMServerToken,
					passKey: api.PMMServerToken,
				},
			}, users...)
		}
	}

	for _, u := range users {
		changed, err := su.add(u.nameKey, u.passKey)
		if err != nil {
			return nil, err
		}

		if changed {
			switch u.nameKey {
			case api.EnvMongoDBBackupUser:
				containers = append(containers, naming.ContainerBackupAgent)
			case api.EnvPMMServerToken:
				containers = append(containers, "pmm-client")
			}
		}
	}

	if su.len() == 0 {
		return containers, nil
	}

	err := r.updateUsers(ctx, cr, su.users, repls)

	return containers, errors.Wrap(err, "mongo: update system users")
}

func (r *ReconcilePerconaServerMongoDB) updateUsers(ctx context.Context, cr *api.PerconaServerMongoDB, users []systemUser, repls []*api.ReplsetSpec) error {
	grp, gCtx := errgroup.WithContext(ctx)

	for i := range repls {
		replset := repls[i]
		grp.Go(func() error {
			client, err := r.mongoClientWithRole(gCtx, cr, replset, api.RoleUserAdmin)
			if err != nil {
				return errors.Wrap(err, "dial:")
			}

			defer func() {
				if err := client.Disconnect(gCtx); err != nil {
					logf.FromContext(ctx).Error(err, "failed to close connection")
				}
			}()

			for _, user := range users {
				if err := user.updateMongo(gCtx, client); err != nil {
					return errors.Wrapf(err, "update users in mongo for replset %s", replset.Name)
				}
			}
			return nil
		})
	}

	return grp.Wait()
}

func (u *systemUser) updateMongo(ctx context.Context, c mongo.Client) error {
	if bytes.Equal(u.currName, u.name) {
		err := c.UpdateUserPass(ctx, "admin", string(u.name), string(u.pass))
		return errors.Wrapf(err, "change password for user %s", u.name)
	}

	err := c.UpdateUser(ctx, string(u.currName), string(u.name), string(u.pass))
	return errors.Wrapf(err, "update user %s -> %s", u.currName, u.name)
}

func sysUsersSecretDataChanged(cr *api.PerconaServerMongoDB, usersSecret, internalSecret *corev1.Secret) (bool, error) {
	newData := getInternalSecretData(cr, usersSecret)
	newDataJSON, err := json.Marshal(newData)
	if err != nil {
		return false, err
	}
	newHash := sha256Hash(newDataJSON)

	oldDataJSON, err := json.Marshal(internalSecret.Data)
	if err != nil {
		return false, err
	}
	oldHash := sha256Hash(oldDataJSON)

	return oldHash != newHash, nil
}

func sha256Hash(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
