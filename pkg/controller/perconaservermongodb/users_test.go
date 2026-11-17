package perconaservermongodb

import (
	"context"
	"fmt"
	"io"
	"maps"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	mongoFake "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo/fake"
)

type fakeUsersMongoProvider struct{}

func (fakeUsersMongoProvider) Mongo(context.Context, *api.PerconaServerMongoDB, *api.ReplsetSpec, api.SystemUserRole, ...mongo.ConfigOption) (mongo.Client, error) {
	return mongoFake.NewClient(), nil
}

func (fakeUsersMongoProvider) Mongos(context.Context, *api.PerconaServerMongoDB, api.SystemUserRole, ...mongo.ConfigOption) (mongo.Client, error) {
	return mongoFake.NewClient(), nil
}

func (fakeUsersMongoProvider) Standalone(context.Context, *api.PerconaServerMongoDB, api.SystemUserRole, string, bool, ...mongo.ConfigOption) (mongo.Client, error) {
	return mongoFake.NewClient(), nil
}

func systemUsersData(backupPassword string) map[string][]byte {
	return map[string][]byte{
		api.EnvMongoDBDatabaseAdminUser:      []byte("databaseAdmin"),
		api.EnvMongoDBDatabaseAdminPassword:  []byte("databaseAdmin123456"),
		api.EnvMongoDBClusterAdminUser:       []byte("clusterAdmin"),
		api.EnvMongoDBClusterAdminPassword:   []byte("clusterAdmin123456"),
		api.EnvMongoDBClusterMonitorUser:     []byte("clusterMonitor"),
		api.EnvMongoDBClusterMonitorPassword: []byte("clusterMonitor123456"),
		api.EnvMongoDBBackupUser:             []byte("backup"),
		api.EnvMongoDBBackupPassword:         []byte(backupPassword),
		api.EnvMongoDBUserAdminUser:          []byte("userAdmin"),
		api.EnvMongoDBUserAdminPassword:      []byte("userAdmin123456"),
	}
}

// usersTestReconciler builds a ready cluster whose users secret has a new
// backup password, so reconcileUsers changes the users and restarts the backup
// agents. exec handles every "kill 1"; it can mark the container restarted, the
// way the kubelet does after a real kill.
func usersTestReconciler(t *testing.T, exec func(r *ReconcilePerconaServerMongoDB, cr *api.PerconaServerMongoDB, pod string) error) (*ReconcilePerconaServerMongoDB, *api.PerconaServerMongoDB, *corev1.Secret) {
	t.Helper()

	cr := connectionStringTestCluster()
	cr.Status.State = api.AppStateReady
	cr.Spec.Secrets = &api.SecretsSpec{Users: "cluster-users"}
	rs := cr.Spec.Replsets[0]
	rs.Size = 3

	users := &corev1.Secret{
		Name: cr.Spec.Secrets.Users, Namespace: cr.Namespace,
		Data: systemUsersData("new-backup-password"),
	}
	internal := &corev1.Secret{
		Name: api.InternalUserSecretName(cr), Namespace: cr.Namespace,
		Data: getInternalSecretData(cr, &corev1.Secret{Data: systemUsersData("old-backup-password")}),
	}

	objects := []client.Object{users, internal, fakeStatefulset(cr, rs, rs.Size, "", "mongod")}
	for _, obj := range fakePodsForRS(cr, rs) {
		pod := obj.(*corev1.Pod)
		pod.Spec.Containers = []corev1.Container{{Name: "mongod"}, {Name: naming.ContainerBackupAgent}}
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
			Name:        naming.ContainerBackupAgent,
			ContainerID: "containerd://agent-0",
			State:       corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		})
		objects = append(objects, pod)
	}

	r := buildFakeClient(objects...)
	r.mongoClientProvider = fakeUsersMongoProvider{}
	r.clientcmd = &mockClientCmd{
		execFunc: func(_ context.Context, pod *corev1.Pod, container string, _ []string, _ io.Reader, _, _ io.Writer, _ bool) error {
			require.Equal(t, naming.ContainerBackupAgent, container)
			return exec(r, cr, pod.Name)
		},
	}
	return r, cr, users
}

// markAgentRestarted does what the kubelet does after "kill 1" in the agent.
func markAgentRestarted(t *testing.T, r *ReconcilePerconaServerMongoDB, cr *api.PerconaServerMongoDB, podName string) {
	t.Helper()

	pod := new(corev1.Pod)
	require.NoError(t, r.client.Get(t.Context(), types.NamespacedName{Name: podName, Namespace: cr.Namespace}, pod))
	status := containerStatus(pod, naming.ContainerBackupAgent)
	status.RestartCount++
	status.ContainerID = fmt.Sprintf("containerd://agent-%d", status.RestartCount)
	require.NoError(t, r.client.Status().Update(t.Context(), pod))
}

// stopAgent puts the agent in CrashLoopBackOff, as after a few quick kills.
func stopAgent(t *testing.T, r *ReconcilePerconaServerMongoDB, cr *api.PerconaServerMongoDB, podName string) {
	t.Helper()

	pod := new(corev1.Pod)
	require.NoError(t, r.client.Get(t.Context(), types.NamespacedName{Name: podName, Namespace: cr.Namespace}, pod))
	status := containerStatus(pod, naming.ContainerBackupAgent)
	status.State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}
	require.NoError(t, r.client.Status().Update(t.Context(), pod))
}

func getInternalUsersSecret(t *testing.T, r *ReconcilePerconaServerMongoDB, cr *api.PerconaServerMongoDB) *corev1.Secret {
	t.Helper()

	got := new(corev1.Secret)
	key := types.NamespacedName{Name: api.InternalUserSecretName(cr), Namespace: cr.Namespace}
	require.NoError(t, r.client.Get(t.Context(), key, got))
	return got
}

// "kill 1" ends the exec session together with the container, so the exec can
// fail although the agent restarted; a retry then finds no container. This
// must count as a restart, the other agents must still be restarted, and the
// internal secret must hold the new credentials. Before the fix the operator
// stopped at the first pod and kept the old credentials, then could not log in
// to MongoDB any more and never synced the users again.
func TestReconcileUsersExecFailsAfterKill(t *testing.T) {
	t.Parallel()

	killed := map[string]int{}
	r, cr, users := usersTestReconciler(t, func(r *ReconcilePerconaServerMongoDB, cr *api.PerconaServerMongoDB, pod string) error {
		killed[pod]++
		if pod != "cluster-rs0-0" {
			markAgentRestarted(t, r, cr, pod)
			return nil
		}
		if killed[pod] == 1 {
			markAgentRestarted(t, r, cr, pod)
			return errors.New("error sending request: stream closed")
		}
		return errors.New(`unable to upgrade connection: container not found ("backup-agent")`)
	})

	require.NoError(t, r.reconcileUsers(t.Context(), cr, cr.Spec.Replsets))
	assert.Equal(t, map[string]int{"cluster-rs0-0": 1, "cluster-rs0-1": 1, "cluster-rs0-2": 1}, killed,
		"every agent is restarted once")
	internal := getInternalUsersSecret(t, r, cr)
	assert.Equal(t, getInternalSecretData(cr, users), internal.Data,
		"the internal secret holds the credentials that MongoDB now has")
	assert.NotContains(t, internal.Annotations, naming.AnnotationPendingRestarts)
}

// An agent that is not running reads the new credentials when it starts, so
// it needs no kill. An exec into it fails with "container not found", and
// before the fix every reconcile retried it and killed the other agents again,
// so all agents stayed in CrashLoopBackOff.
func TestReconcileUsersSkipsStoppedAgent(t *testing.T) {
	t.Parallel()

	killed := map[string]int{}
	r, cr, _ := usersTestReconciler(t, func(r *ReconcilePerconaServerMongoDB, cr *api.PerconaServerMongoDB, pod string) error {
		killed[pod]++
		if pod == "cluster-rs0-1" {
			return errors.New(`unable to upgrade connection: container not found ("backup-agent")`)
		}
		markAgentRestarted(t, r, cr, pod)
		return nil
	})
	stopAgent(t, r, cr, "cluster-rs0-1")

	require.NoError(t, r.reconcileUsers(t.Context(), cr, cr.Spec.Replsets))
	assert.Equal(t, map[string]int{"cluster-rs0-0": 1, "cluster-rs0-2": 1}, killed,
		"only the running agents are restarted")
	assert.NotContains(t, getInternalUsersSecret(t, r, cr).Annotations, naming.AnnotationPendingRestarts)
}

// When an agent really cannot be restarted, the error is reported, the other
// agents are still restarted, the operator keeps the credentials that MongoDB
// has, and the next reconcile restarts only that agent again.
func TestReconcileUsersAgentRestartFails(t *testing.T) {
	t.Parallel()

	broken := true
	killed := map[string]int{}
	r, cr, users := usersTestReconciler(t, func(r *ReconcilePerconaServerMongoDB, cr *api.PerconaServerMongoDB, pod string) error {
		killed[pod]++
		if pod == "cluster-rs0-1" && broken {
			return errors.New("error dialing backend: timeout")
		}
		markAgentRestarted(t, r, cr, pod)
		return nil
	})

	err := r.reconcileUsers(t.Context(), cr, cr.Spec.Replsets)
	require.ErrorContains(t, err, "cluster-rs0-1")
	assert.Equal(t, 1, killed["cluster-rs0-0"])
	assert.Equal(t, 1, killed["cluster-rs0-2"])
	internal := getInternalUsersSecret(t, r, cr)
	assert.Equal(t, getInternalSecretData(cr, users), internal.Data,
		"the internal secret holds the credentials that MongoDB now has")
	assert.Equal(t, naming.ContainerBackupAgent+"/cluster-rs0-1", internal.Annotations[naming.AnnotationPendingRestarts])

	broken = false
	before := maps.Clone(killed)
	require.NoError(t, r.reconcileUsers(t.Context(), cr, cr.Spec.Replsets))
	before["cluster-rs0-1"]++
	assert.Equal(t, before, killed, "the next reconcile retries only the failed restart")
	assert.NotContains(t, getInternalUsersSecret(t, r, cr).Annotations, naming.AnnotationPendingRestarts)
}

func TestContainerRestarted(t *testing.T) {
	t.Parallel()

	running := corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
	before := &corev1.ContainerStatus{Name: naming.ContainerBackupAgent, ContainerID: "containerd://a", RestartCount: 2, State: running}

	tests := map[string]struct {
		before *corev1.ContainerStatus
		now    corev1.ContainerStatus
		want   bool
	}{
		"no status before the kill": {
			before: nil,
			now:    corev1.ContainerStatus{Name: naming.ContainerBackupAgent, RestartCount: 3},
			want:   false,
		},
		"restart count went up": {
			before: before,
			now:    corev1.ContainerStatus{Name: naming.ContainerBackupAgent, ContainerID: "containerd://a", RestartCount: 3, State: running},
			want:   true,
		},
		"container replaced": {
			before: before,
			now:    corev1.ContainerStatus{Name: naming.ContainerBackupAgent, ContainerID: "containerd://b", RestartCount: 2, State: running},
			want:   true,
		},
		"container stopped": {
			before: before,
			now: corev1.ContainerStatus{
				Name: naming.ContainerBackupAgent, ContainerID: "containerd://a", RestartCount: 2,
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
			},
			want: true,
		},
		"same container": {
			before: before,
			now:    *before,
			want:   false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pod := &corev1.Pod{
				Name: "pod", Namespace: "ns",
				Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{tt.now}},
			}
			r := buildFakeClient(pod)
			assert.Equal(t, tt.want, r.containerRestarted(t.Context(), pod, naming.ContainerBackupAgent, tt.before))
		})
	}
}
