package perconaservermongodb

import (
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	mongoFake "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo/fake"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestMongoInitAdminUserEscaping(t *testing.T) {
	const prefix = `db.getSiblingDB("admin").createUser(`

	for _, tc := range []struct {
		name string
		user string
		pwd  string
	}{
		{name: "double quote", user: "userAdmin", pwd: `literal"quote`},
		{name: "single quote", user: "user'Admin", pwd: "literal'quote"},
		{name: "backslash and newline", user: "userAdmin", pwd: "back\\slash\nnext"},
		{name: "shell metacharacters", user: "userAdmin", pwd: "$(false);`false`$HOME"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quotedExpression, err := mongoInitAdminUser(tc.user, tc.pwd)
			require.NoError(t, err)

			actual, err := exec.Command("sh", "-c", `set -- `+quotedExpression+`; printf '%s' "$1"`).Output()
			require.NoError(t, err)

			expression := string(actual)
			require.True(t, strings.HasPrefix(expression, prefix), "unexpected JavaScript: %q", expression)
			require.True(t, strings.HasSuffix(expression, ")"), "unexpected JavaScript: %q", expression)

			var userDoc struct {
				User  string   `json:"user"`
				Pwd   string   `json:"pwd"`
				Roles []string `json:"roles"`
			}
			require.NoError(t, json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(expression, prefix), ")")), &userDoc))
			assert.Equal(t, tc.user, userDoc.User)
			assert.Equal(t, tc.pwd, userDoc.Pwd)
			assert.Equal(t, []string{"userAdminAnyDatabase"}, userDoc.Roles)
		})
	}
}

func TestHandleReplsetInitPassesConfigWithoutShell(t *testing.T) {
	const replsetName = "rs'$(echo name)"
	const tagValue = "literal\"quote'$(echo tag)\nnext"

	exec := &replsetInitExecRecorder{authCheckErr: errors.New("Authentication failed")}
	r, cr, rs := setupReplsetInitTest(t, &initMongoClientProvider{}, exec)
	rs.Configuration = api.MongoConfiguration("replication:\n  replSetName: \"" + replsetName + "\"\n")
	podName := cr.Name + "-" + rs.Name + "-0"
	rs.ReplsetOverrides = api.ReplsetOverrides{
		podName: {Tags: map[string]string{"special": tagValue}},
	}

	_, _, err := r.handleReplsetInit(context.Background(), cr, rs, []corev1.Pod{*fakeMongodPod(cr, rs, podName)})
	require.NoError(t, err)
	require.NotEmpty(t, exec.initCommand)
	assert.Equal(t, "mongosh", exec.initCommand[0])
	assert.Contains(t, exec.initCommand, "--port")

	const prefix = "rs.initiate("
	require.True(t, strings.HasPrefix(exec.initInput, prefix), "unexpected JavaScript: %q", exec.initInput)
	require.True(t, strings.HasSuffix(exec.initInput, ")\n"), "unexpected JavaScript: %q", exec.initInput)

	var config struct {
		ID      string `json:"_id"`
		Version int    `json:"version"`
		Members []struct {
			Tags map[string]string `json:"tags"`
		} `json:"members"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(exec.initInput, prefix), ")\n")), &config))
	assert.Equal(t, replsetName, config.ID)
	assert.Equal(t, 1, config.Version)
	require.Len(t, config.Members, 1)
	assert.Equal(t, tagValue, config.Members[0].Tags["special"])
}

// initMongoClientProvider lets a test decide, per role, whether obtaining a
// mongo client succeeds. handleReplsetInit is only reached when the
// RoleClusterAdmin connection fails, and createOrUpdateSystemUsers connects as
// RoleUserAdmin.
type initMongoClientProvider struct {
	clusterAdminErr error
	userAdminErr    error
	cr              *api.PerconaServerMongoDB
	pods            []client.Object
}

func (p *initMongoClientProvider) Mongo(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole) (mongo.Client, error) {
	if role == api.RoleUserAdmin {
		if p.userAdminErr != nil {
			return nil, p.userAdminErr
		}
		return &fakeMongoClient{pods: p.pods, cr: p.cr, connectionCount: new(int), Client: mongoFake.NewClient()}, nil
	}
	if p.clusterAdminErr != nil {
		return nil, p.clusterAdminErr
	}
	return &fakeMongoClient{pods: p.pods, cr: p.cr, connectionCount: new(int), Client: mongoFake.NewClient()}, nil
}

func (p *initMongoClientProvider) Mongos(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole) (mongo.Client, error) {
	return &fakeMongoClient{pods: p.pods, cr: p.cr, connectionCount: new(int), Client: mongoFake.NewClient()}, nil
}

func (p *initMongoClientProvider) Standalone(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole, host string, tlsEnabled bool) (mongo.Client, error) {
	return &fakeMongoClient{pods: p.pods, cr: p.cr, connectionCount: new(int), Client: mongoFake.NewClient()}, nil
}

// replsetInitExecRecorder mocks the in-pod command execution performed by
// handleReplsetInit and records how many times createUser/auth-check were run.
type replsetInitExecRecorder struct {
	createUserErr             error // returned by createUser on every call
	createUserErrAfterFirst   error // if set, returned by createUser on calls after the first (e.g. user already exists / auth now required)
	authCheckErr              error
	authCheckErrAfterFirstSet bool
	authCheckErrAfterFirst    error
	createUserCalls           atomic.Int32
	authCheckCalls            atomic.Int32
	initCommand               []string
	initInput                 string
}

func (m *replsetInitExecRecorder) Exec(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
	joined := strings.Join(command, " ")
	switch {
	case strings.Contains(joined, "--version"):
		if stdout != nil {
			_, _ = stdout.Write([]byte("db version v7.0.0"))
		}
		return nil
	case stdin != nil:
		input, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		m.initCommand = append([]string(nil), command...)
		m.initInput = string(input)
		return nil
	case strings.Contains(joined, "isWritablePrimary"):
		if stdout != nil {
			_, _ = stdout.Write([]byte("true"))
		}
		return nil
	case strings.Contains(joined, "createUser"):
		n := m.createUserCalls.Add(1)
		if n > 1 && m.createUserErrAfterFirst != nil {
			return m.createUserErrAfterFirst
		}
		return m.createUserErr
	case strings.Contains(joined, "connectionStatus"):
		n := m.authCheckCalls.Add(1)
		if n > 1 && m.authCheckErrAfterFirstSet {
			return m.authCheckErrAfterFirst
		}
		return m.authCheckErr
	default:
		return nil
	}
}

func fakeMongodPod(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, name string) *corev1.Pod {
	ls := naming.RSLabels(cr, rs)
	ls[naming.LabelKubernetesComponent] = naming.ComponentMongod

	return &corev1.Pod{
		Name:      name,
		Namespace: cr.Namespace,
		Labels:    ls,
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "mongod"}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:  "mongod",
					Ready: true,
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()},
					},
				},
			},
			Conditions: []corev1.PodCondition{
				{Type: corev1.ContainersReady, Status: corev1.ConditionTrue},
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

func internalUsersSecret(cr *api.PerconaServerMongoDB) *corev1.Secret {
	return &corev1.Secret{
		Name:      api.InternalUserSecretName(cr),
		Namespace: cr.Namespace,
		Data: map[string][]byte{
			api.EnvMongoDBUserAdminUser:          []byte("userAdmin"),
			api.EnvMongoDBUserAdminPassword:      []byte("userAdmin123"),
			api.EnvMongoDBClusterAdminUser:       []byte("clusterAdmin"),
			api.EnvMongoDBClusterAdminPassword:   []byte("clusterAdmin123"),
			api.EnvMongoDBClusterMonitorUser:     []byte("clusterMonitor"),
			api.EnvMongoDBClusterMonitorPassword: []byte("clusterMonitor123"),
			api.EnvMongoDBBackupUser:             []byte("backup"),
			api.EnvMongoDBBackupPassword:         []byte("backup123"),
			api.EnvMongoDBDatabaseAdminUser:      []byte("databaseAdmin"),
			api.EnvMongoDBDatabaseAdminPassword:  []byte("databaseAdmin123"),
		},
	}
}

func newReplsetInitCR() *api.PerconaServerMongoDB {
	return &api.PerconaServerMongoDB{
		Name:       "init-test",
		Namespace:  "psmdb",
		Generation: 1,
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
			Image:     "percona/percona-server-mongodb:latest",
			Unsafe: api.UnsafeFlags{
				ReplsetSize: true,
			},
			Replsets: []*api.ReplsetSpec{
				{
					Name: "rs0",
					Size: 1,
				},
			},
		},
		Status: api.PerconaServerMongoDBStatus{
			Replsets: map[string]api.ReplsetStatus{},
		},
	}
}

// readInitializedFromAPI reads the persisted (Kubernetes) initialized flag for
// the given replset, proving the status was written and not just kept
// in-memory.
func readInitializedFromAPI(t *testing.T, r *ReconcilePerconaServerMongoDB, cr *api.PerconaServerMongoDB, rsName string) bool {
	t.Helper()
	persisted := &api.PerconaServerMongoDB{}
	require.NoError(t, r.client.Get(context.Background(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, persisted))
	return persisted.Status.Replsets[rsName].Initialized
}

func hasInitCondition(cr *api.PerconaServerMongoDB, rsName string) bool {
	for _, c := range cr.Status.Conditions {
		if c.Type == api.AppStateInit && c.Message == rsName && c.Status == api.ConditionTrue {
			return true
		}
	}
	return false
}

func setupReplsetInitTest(t *testing.T, provider *initMongoClientProvider, exec ClientCmd) (*ReconcilePerconaServerMongoDB, *api.PerconaServerMongoDB, *api.ReplsetSpec) {
	t.Helper()

	ctx := context.Background()

	cr := newReplsetInitCR()
	cr.Spec.Replsets[0].VolumeSpec = fakeVolumeSpec(t)
	require.NoError(t, cr.CheckNSetDefaults(ctx, version.PlatformKubernetes))
	// CheckNSetDefaults may create a copy-friendly map; ensure the status map is
	// present for the reconcile path that writes into it.
	cr.Status.Replsets = map[string]api.ReplsetStatus{}

	rs := cr.Spec.Replsets[0]

	pod := fakeMongodPod(cr, rs, cr.Name+"-"+rs.Name+"-0")

	objs := []client.Object{
		cr,
		fakeStatefulset(cr, rs, rs.Size, "rev", naming.ComponentMongod),
		pod,
		internalUsersSecret(cr),
	}

	r := buildFakeClient(objs...)
	provider.cr = cr
	provider.pods = []client.Object{pod}
	r.mongoClientProvider = provider
	r.clientcmd = exec
	r.serverVersion = &version.ServerVersion{Platform: version.PlatformKubernetes}

	return r, cr, rs
}

// TestReconcileClusterInitRecoversAfterSystemUsersFailure reproduces the
// production scenario: handleReplsetInit creates userAdmin, then
// createOrUpdateSystemUsers fails transiently (e.g. DNS not ready). The replset
// must NOT be marked initialized in that case (otherwise the next reconcile
// connects as clusterAdmin, which does not exist yet, and deadlocks). On the
// next reconcile, once the transient error clears, init must recover:
// handleReplsetInit is idempotent (userAdmin authenticates, so createUser is
// skipped), createOrUpdateSystemUsers succeeds, and only then is initialized
// persisted.
func TestReconcileClusterInitRecoversAfterSystemUsersFailure(t *testing.T) {
	logf.SetLogger(zap.New(zap.WriteTo(io.Discard)))
	ctx := context.Background()

	exec := &replsetInitExecRecorder{
		// First createUser (initial creation) succeeds; subsequent ones fail as
		// they would on a real cluster once auth is enabled and the user exists.
		createUserErrAfterFirst:   errors.New("MongoServerError: Command createUser requires authentication"),
		authCheckErr:              errors.New("Authentication failed"),
		authCheckErrAfterFirstSet: true,
	}
	provider := &initMongoClientProvider{
		clusterAdminErr: errors.New("dial: no reachable servers"),
		userAdminErr:    errors.New("failed to get mongo client: no such host"), // transient failure
	}

	r, cr, rs := setupReplsetInitTest(t, provider, exec)

	// Reconcile 1: init succeeds, createOrUpdateSystemUsers fails transiently.
	_, _, err := r.reconcileCluster(ctx, cr, rs, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create system users")

	// Must NOT be initialized yet - otherwise the cluster would deadlock.
	assert.False(t, cr.Status.Replsets[rs.Name].Initialized, "in-memory initialized must stay false on failure")
	assert.False(t, readInitializedFromAPI(t, r, cr, rs.Name), "persisted initialized must stay false on failure")
	require.Equal(t, int32(1), exec.createUserCalls.Load(), "createUser created the user once")

	// Transient failure clears.
	provider.userAdminErr = nil

	// Reconcile 2: re-enters init branch (still not initialized). userAdmin
	// authenticates, so createUser is skipped and createOrUpdateSystemUsers
	// succeeds.
	_, members, err := r.reconcileCluster(ctx, cr, rs, nil)
	require.NoError(t, err)
	assert.True(t, cr.Status.Replsets[rs.Name].Initialized, "initialized after successful system user creation")
	assert.Contains(t, members, cr.Name+"-"+rs.Name+"-0")
	assert.True(t, hasInitCondition(cr, rs.Name))
	assert.Equal(t, int32(1), exec.createUserCalls.Load(), "createUser should not be retried when userAdmin already authenticates")
	assert.Equal(t, int32(2), exec.authCheckCalls.Load(), "auth check used before createUser on each init attempt")
}

// TestReconcileClusterInitHappyPath verifies the unchanged happy path: when
// both init and createOrUpdateSystemUsers succeed, the status is initialized,
// the primary member is recorded and the Init condition is added.
func TestReconcileClusterInitHappyPath(t *testing.T) {
	logf.SetLogger(zap.New(zap.WriteTo(io.Discard)))
	ctx := context.Background()

	exec := &replsetInitExecRecorder{
		authCheckErr: errors.New("Authentication failed"),
	}
	provider := &initMongoClientProvider{
		clusterAdminErr: errors.New("dial: no reachable servers"),
		// userAdminErr nil -> createOrUpdateSystemUsers succeeds
	}

	r, cr, rs := setupReplsetInitTest(t, provider, exec)

	_, members, err := r.reconcileCluster(ctx, cr, rs, nil)
	require.NoError(t, err)

	assert.True(t, cr.Status.Replsets[rs.Name].Initialized)
	assert.Contains(t, members, cr.Name+"-"+rs.Name+"-0")
	assert.Equal(t, mongo.MemberStatePrimary, members[cr.Name+"-"+rs.Name+"-0"].State)
	assert.True(t, hasInitCondition(cr, rs.Name))
	assert.Equal(t, int32(1), exec.createUserCalls.Load())
	assert.Equal(t, int32(1), exec.authCheckCalls.Load())
}

// TestHandleReplsetInitIdempotentAdminUser covers the idempotency of admin user
// creation: when userAdmin can already authenticate, createUser is skipped; when
// createUser fails but authentication succeeds immediately after, init recovers;
// when authentication also fails, the real error is returned.
func TestHandleReplsetInitIdempotentAdminUser(t *testing.T) {
	logf.SetLogger(zap.New(zap.WriteTo(io.Discard)))
	ctx := context.Background()

	t.Run("skips creation when userAdmin can authenticate", func(t *testing.T) {
		exec := &replsetInitExecRecorder{
			authCheckErr: nil, // authentication succeeds
		}
		r, cr, rs := setupReplsetInitTest(t, &initMongoClientProvider{}, exec)

		pods := []corev1.Pod{*fakeMongodPod(cr, rs, cr.Name+"-"+rs.Name+"-0")}
		pod, primary, err := r.handleReplsetInit(ctx, cr, rs, pods)
		require.NoError(t, err)
		require.NotNil(t, pod)
		require.NotNil(t, primary)
		assert.Equal(t, mongo.MemberStatePrimary, primary.State)
		assert.Equal(t, int32(0), exec.createUserCalls.Load(), "createUser should be skipped when userAdmin already authenticates")
		assert.Equal(t, int32(1), exec.authCheckCalls.Load())
	})

	t.Run("recovers when userAdmin authenticates after createUser error", func(t *testing.T) {
		exec := &replsetInitExecRecorder{
			authCheckErr:              errors.New("Authentication failed"),
			authCheckErrAfterFirstSet: true,
			authCheckErrAfterFirst:    nil,
			createUserErr:             errors.New("MongoServerError: Command createUser requires authentication"),
		}
		r, cr, rs := setupReplsetInitTest(t, &initMongoClientProvider{}, exec)

		pods := []corev1.Pod{*fakeMongodPod(cr, rs, cr.Name+"-"+rs.Name+"-0")}
		pod, primary, err := r.handleReplsetInit(ctx, cr, rs, pods)
		require.NoError(t, err)
		require.NotNil(t, pod)
		require.NotNil(t, primary)
		assert.Equal(t, mongo.MemberStatePrimary, primary.State)
		assert.Equal(t, int32(1), exec.createUserCalls.Load())
		assert.Equal(t, int32(2), exec.authCheckCalls.Load(), "auth check should be attempted before createUser and after createUser fails")
	})

	t.Run("returns error when userAdmin cannot authenticate", func(t *testing.T) {
		exec := &replsetInitExecRecorder{
			createUserErr: errors.New("MongoServerError: some real failure"),
			authCheckErr:  errors.New("Authentication failed"),
		}
		r, cr, rs := setupReplsetInitTest(t, &initMongoClientProvider{}, exec)

		pods := []corev1.Pod{*fakeMongodPod(cr, rs, cr.Name+"-"+rs.Name+"-0")}
		_, _, err := r.handleReplsetInit(ctx, cr, rs, pods)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exec add admin user")
		assert.Equal(t, int32(2), exec.authCheckCalls.Load(), "auth check should be attempted before createUser and before giving up")
	})

	t.Run("returns auth check exec error before createUser", func(t *testing.T) {
		exec := &replsetInitExecRecorder{
			authCheckErr: errors.New("exec failed"),
		}
		r, cr, rs := setupReplsetInitTest(t, &initMongoClientProvider{}, exec)

		pods := []corev1.Pod{*fakeMongodPod(cr, rs, cr.Name+"-"+rs.Name+"-0")}
		_, _, err := r.handleReplsetInit(ctx, cr, rs, pods)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exec userAdmin authentication check")
		assert.Equal(t, int32(0), exec.createUserCalls.Load(), "createUser should not run when auth check itself fails")
		assert.Equal(t, int32(1), exec.authCheckCalls.Load())
	})
}
