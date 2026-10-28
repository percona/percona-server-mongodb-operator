package client

import (
	"context"
	stderrors "errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/clustersync"
)

type stubExec struct {
	stdout []byte
	stderr []byte
	err    error

	cmd []string
}

func (s *stubExec) Exec(_ context.Context, _ *corev1.Pod, _ string, cmd []string, _ io.Reader, stdout, stderr io.Writer, _ bool) error {
	s.cmd = append([]string(nil), cmd...)
	if s.stdout != nil {
		_, _ = stdout.Write(s.stdout)
	}
	if s.stderr != nil {
		_, _ = stderr.Write(s.stderr)
	}
	return s.err
}

type execResult struct {
	stdout []byte
	stderr []byte
	err    error
}

// scriptedExec answers per pod so a test can model an HA group where only
// one instance holds the lease, and records the order pods were tried in.
type scriptedExec struct {
	byPod map[string]execResult

	pods []string
}

func (s *scriptedExec) Exec(_ context.Context, p *corev1.Pod, _ string, _ []string, _ io.Reader, stdout, stderr io.Writer, _ bool) error {
	s.pods = append(s.pods, p.Name)
	res := s.byPod[p.Name]
	if res.stdout != nil {
		_, _ = stdout.Write(res.stdout)
	}
	if res.stderr != nil {
		_, _ = stderr.Write(res.stderr)
	}
	return res.err
}

func standbyResult(activeHost string) execResult {
	return execResult{
		stdout: []byte(`{"ok":false,"error":"not_active","role":"STANDBY","message":"This instance is STANDBY. Active is running on ` + activeHost + `"}`),
		err:    stderrors.New("exit status 1"),
	}
}

func activeResult() execResult {
	return execResult{stdout: []byte(`{"state":"running","role":"ACTIVE"}`)}
}

func pod(name string, instance string, ready bool) *corev1.Pod {
	return &corev1.Pod{
		Name:      name,
		Namespace: "ns",
		Labels: map[string]string{
			naming.LabelKubernetesInstance:  instance,
			naming.LabelKubernetesComponent: clustersync.ComponentPCSM,
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{Name: clustersync.ContainerName, Ready: ready}},
		},
	}
}

func readyPod() *corev1.Pod {
	return pod("pcsm-0", "cr", true)
}

func newClient(exec podExecer, pods ...*corev1.Pod) *Client {
	objs := make([]k8sclient.Object, 0, len(pods))
	for _, p := range pods {
		objs = append(objs, p)
	}
	return &Client{
		k8s:  fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(objs...).Build(),
		exec: exec,
		cr:   &psmdbv1.PerconaServerMongoDBClusterSync{Name: "cr", Namespace: "ns"},
	}
}

func TestClient_Verbs(t *testing.T) {
	tests := map[string]struct {
		stub    stubExec
		call    func(context.Context, *Client) error
		wantCmd []string
		wantErr string
	}{
		"pause": {
			stub:    stubExec{stdout: []byte(`{}`)},
			call:    func(ctx context.Context, c *Client) error { return c.Pause(ctx) },
			wantCmd: []string{"pcsm", "pause"},
		},
		"finalize": {
			stub:    stubExec{stdout: []byte(`{}`)},
			call:    func(ctx context.Context, c *Client) error { return c.Finalize(ctx) },
			wantCmd: []string{"pcsm", "finalize"},
		},
		"start with no namespace options": {
			stub:    stubExec{stdout: []byte(`{}`)},
			call:    func(ctx context.Context, c *Client) error { return c.Start(ctx, StartOptions{}) },
			wantCmd: []string{"pcsm", "start"},
		},
		"start passes excludeNamespaces as csv flag": {
			stub: stubExec{stdout: []byte(`{}`)},
			call: func(ctx context.Context, c *Client) error {
				return c.Start(ctx, StartOptions{ExcludeNamespaces: []string{"admin", "local"}})
			},
			wantCmd: []string{"pcsm", "start", "--exclude-namespaces=admin,local"},
		},
		"start passes includeNamespaces as csv flag": {
			stub: stubExec{stdout: []byte(`{}`)},
			call: func(ctx context.Context, c *Client) error {
				return c.Start(ctx, StartOptions{IncludeNamespaces: []string{"db1.*", "db2.coll"}})
			},
			wantCmd: []string{"pcsm", "start", "--include-namespaces=db1.*,db2.coll"},
		},
		"start passes both include and exclude in order": {
			stub: stubExec{stdout: []byte(`{}`)},
			call: func(ctx context.Context, c *Client) error {
				return c.Start(ctx, StartOptions{
					IncludeNamespaces: []string{"db1.*"},
					ExcludeNamespaces: []string{"db1.users"},
				})
			},
			wantCmd: []string{"pcsm", "start", "--include-namespaces=db1.*", "--exclude-namespaces=db1.users"},
		},
		"resume clean has no flags": {
			stub:    stubExec{stdout: []byte(`{}`)},
			call:    func(ctx context.Context, c *Client) error { return c.Resume(ctx, false) },
			wantCmd: []string{"pcsm", "resume"},
		},
		"resume fromFailure passes flag": {
			stub:    stubExec{stdout: []byte(`{}`)},
			call:    func(ctx context.Context, c *Client) error { return c.Resume(ctx, true) },
			wantCmd: []string{"pcsm", "resume", "--from-failure"},
		},
		"exec error wraps underlying error and stderr": {
			stub: stubExec{
				stderr: []byte("Error: connection refused"),
				err:    stderrors.New("exit status 1"),
			},
			call:    func(ctx context.Context, c *Client) error { return c.Pause(ctx) },
			wantErr: "exec pcsm pause on pcsm-0: Error: connection refused: exit status 1",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stub := tc.stub
			err := tc.call(t.Context(), newClient(&stub, readyPod()))

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantCmd, stub.cmd)
		})
	}
}

func TestClient_Status(t *testing.T) {
	tests := map[string]struct {
		stub    stubExec
		want    Status
		wantErr string
	}{
		"replicating decodes all fields": {
			stub: stubExec{stdout: []byte(`{"state":"replicating","lagTimeSeconds":3,"info":"ok"}`)},
			want: Status{State: "replicating", LagTimeSeconds: 3, Info: "ok"},
		},
		"failed surfaces error field": {
			stub: stubExec{stdout: []byte(`{"state":"failed","error":"source unreachable"}`)},
			want: Status{State: "failed", Error: "source unreachable"},
		},
		"failed with non-zero exit still returns parsed status": {
			stub: stubExec{
				stdout: []byte(`{"state":"failed","error":"connection refused"}`),
				stderr: []byte("Error: connection refused"),
				err:    stderrors.New("exit status 1"),
			},
			want: Status{State: "failed", Error: "connection refused"},
		},
		"non-zero exit with empty stdout returns exec error": {
			stub: stubExec{
				stderr: []byte("boom"),
				err:    stderrors.New("exit status 1"),
			},
			wantErr: "exec pcsm status on pcsm-0: boom: exit status 1",
		},
		"non-zero exit with non-JSON stdout returns exec error": {
			stub: stubExec{
				stdout: []byte("panic: runtime error"),
				stderr: []byte("boom"),
				err:    stderrors.New("exit status 2"),
			},
			wantErr: "exec pcsm status on pcsm-0: boom: exit status 2",
		},
		"stateless JSON with non-zero exit returns the exec error": {
			stub: stubExec{
				stdout: []byte(`{"error":"not a pcsm payload"}`),
				stderr: []byte("boom"),
				err:    stderrors.New("exit status 1"),
			},
			wantErr: "exec pcsm status on pcsm-0: boom: exit status 1",
		},
		"bad JSON wraps decode error": {
			stub:    stubExec{stdout: []byte(`not-json`)},
			wantErr: "decode pcsm status output",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stub := tc.stub
			got, err := newClient(&stub, readyPod()).Status(t.Context())

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, []string{"pcsm", "status"}, stub.cmd)
		})
	}
}

// TestClient_ReadyPodSelection covers which pods are eligible targets at
// all, before any ACTIVE/STANDBY routing happens: terminating pods, pods
// whose container is not Ready, and pods belonging to a different CR must
// never receive a pcsm command.
func TestClient_ReadyPodSelection(t *testing.T) {
	now := metav1.Now()
	terminating := pod("pcsm-terminating", "cr", true)
	terminating.DeletionTimestamp = &now
	terminating.Finalizers = []string{"stub.test/keep"}

	otherCR := pod("pcsm-other", "different-cr", true)
	unready := pod("pcsm-unready", "cr", false)
	ready := pod("pcsm-0", "cr", true)

	tests := map[string]struct {
		pods    []*corev1.Pod
		wantErr error
	}{
		"no pods at all":                    {pods: nil, wantErr: ErrPCSMNotReady},
		"only unready pods":                 {pods: []*corev1.Pod{unready}, wantErr: ErrPCSMNotReady},
		"only terminating pods":             {pods: []*corev1.Pod{terminating}, wantErr: ErrPCSMNotReady},
		"only pods belonging to another CR": {pods: []*corev1.Pod{otherCR}, wantErr: ErrPCSMNotReady},
		"ready pod is selected":             {pods: []*corev1.Pod{ready}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stub := &stubExec{stdout: []byte(`{}`)}
			err := newClient(stub, tc.pods...).Pause(t.Context())

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Empty(t, stub.cmd, "exec must not run when no ready pod was selected")
				return
			}
			require.NoError(t, err)
		})
	}
}

// TestClient_RoutesToActiveInstance is the core HA test.
//
// Under HA only one PCSM instance holds the lease on the target; every
// other instance answers an operational command with HTTP 409 and
// {"error":"not_active"}. The client therefore has to find the writer
// rather than talk to whichever pod the API server listed first.
//
// Two rules are load-bearing and both are asserted here:
//
//   - Only a not_active reply advances to the next pod. Any other failure
//     is the active instance's own answer, and retrying it elsewhere could
//     run a start or finalize twice — the one outcome that is worse than
//     failing the reconcile.
//   - The last known active instance is tried first. Without that, every
//     reconcile (every 5s) would exec into each standby before reaching
//     the writer, which scales the exec cost with group size for no gain.
func TestClient_RoutesToActiveInstance(t *testing.T) {
	const (
		pod0 = "pcsm-0"
		pod1 = "pcsm-1"
		pod2 = "pcsm-2"
	)
	pause := func(ctx context.Context, c *Client) error { return c.Pause(ctx) }
	status := func(ctx context.Context, c *Client) error { _, err := c.Status(ctx); return err }

	tests := map[string]struct {
		byPod      map[string]execResult
		seedActive string
		call       func(context.Context, *Client) error
		wantPods   []string
		wantActive string
		wantErr    error
		wantErrMsg string
	}{
		"first instance is active: one exec, no probing": {
			byPod:      map[string]execResult{pod0: activeResult(), pod1: standbyResult(pod0), pod2: standbyResult(pod0)},
			call:       pause,
			wantPods:   []string{pod0},
			wantActive: pod0,
		},
		"standbys are skipped until the active instance answers": {
			byPod:      map[string]execResult{pod0: standbyResult(pod2), pod1: standbyResult(pod2), pod2: activeResult()},
			call:       pause,
			wantPods:   []string{pod0, pod1, pod2},
			wantActive: pod2,
		},
		"whole group standby (mid-election) reports no active instance": {
			byPod:    map[string]execResult{pod0: standbyResult(""), pod1: standbyResult(""), pod2: standbyResult("")},
			call:     pause,
			wantPods: []string{pod0, pod1, pod2},
			wantErr:  ErrNoActivePCSM,
		},
		"seeded active instance is tried first": {
			byPod:      map[string]execResult{pod0: standbyResult(pod2), pod1: standbyResult(pod2), pod2: activeResult()},
			seedActive: pod2,
			call:       pause,
			wantPods:   []string{pod2},
			wantActive: pod2,
		},
		"seeded instance that lost the lease falls through to the new active": {
			byPod:      map[string]execResult{pod0: activeResult(), pod1: standbyResult(pod0), pod2: standbyResult(pod0)},
			seedActive: pod2,
			call:       pause,
			wantPods:   []string{pod2, pod0},
			wantActive: pod0,
		},
		"a transport failure on a write verb is never retried elsewhere": {
			byPod: map[string]execResult{
				pod0: {stderr: []byte("connection refused"), err: stderrors.New("exit status 1")},
				pod1: activeResult(),
				pod2: activeResult(),
			},
			call:     pause,
			wantPods: []string{pod0},
			// The exec may never have reached PCSM, so pod0 is no
			// evidence of who holds the lease.
			wantActive: "",
			wantErrMsg: "exec pcsm pause on pcsm-0: connection refused: exit status 1",
		},
		"a failure PCSM itself reported caches the instance that answered": {
			byPod: map[string]execResult{
				pod0: {stdout: []byte(`{"state":"failed","error":"sync is already running"}`), err: stderrors.New("exit status 1")},
				pod1: activeResult(),
				pod2: activeResult(),
			},
			call:       pause,
			wantPods:   []string{pod0},
			wantActive: pod0,
			wantErrMsg: "exec pcsm pause on pcsm-0: : exit status 1",
		},
		"status skips a transport failure and finds the active instance": {
			byPod: map[string]execResult{
				pod0: {stderr: []byte("container not running"), err: stderrors.New("exit status 126")},
				pod1: activeResult(),
				pod2: standbyResult(pod1),
			},
			call:       status,
			wantPods:   []string{pod0, pod1},
			wantActive: pod1,
		},
		"status on an unreachable group surfaces the transport error": {
			byPod: map[string]execResult{
				pod0: {stderr: []byte("container not running"), err: stderrors.New("exit status 126")},
				pod1: {stderr: []byte("container not running"), err: stderrors.New("exit status 126")},
				pod2: {stderr: []byte("container not running"), err: stderrors.New("exit status 126")},
			},
			call:       status,
			wantPods:   []string{pod0, pod1, pod2},
			wantErrMsg: "exec pcsm status on pcsm-0: container not running: exit status 126",
		},
		"status is routed the same way as the write verbs": {
			byPod:      map[string]execResult{pod0: standbyResult(pod1), pod1: activeResult(), pod2: standbyResult(pod1)},
			call:       status,
			wantPods:   []string{pod0, pod1},
			wantActive: pod1,
		},
		"status on an all-standby group surfaces ErrNoActivePCSM, not a decode error": {
			byPod:   map[string]execResult{pod0: standbyResult(""), pod1: standbyResult(""), pod2: standbyResult("")},
			call:    status,
			wantErr: ErrNoActivePCSM,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			exec := &scriptedExec{byPod: tc.byPod}
			c := newClient(exec, pod(pod0, "cr", true), pod(pod1, "cr", true), pod(pod2, "cr", true))
			if tc.seedActive != "" {
				c = c.WithActivePod(tc.seedActive)
			}

			err := tc.call(t.Context(), c)

			switch {
			case tc.wantErr != nil:
				require.ErrorIs(t, err, tc.wantErr)
			case tc.wantErrMsg != "":
				require.EqualError(t, err, tc.wantErrMsg)
			default:
				require.NoError(t, err)
			}
			if tc.wantPods != nil {
				assert.Equal(t, tc.wantPods, exec.pods, "unexpected exec order")
			}
			assert.Equal(t, tc.wantActive, c.ActiveInstance())
		})
	}
}

// TestNotActive covers the 409 detection itself. The JSON body is the
// documented contract; the substring fallbacks exist because the pcsm CLI
// may surface the error as plain text on stderr instead of relaying the
// body, and mistaking that for a hard failure would strand the group.
// TestClient_ListFailure covers the branch where the pod lookup itself
// fails. It must surface as a wrapped error rather than being mistaken
// for an empty group, which isPCSMUnreachable would swallow as transient.
func TestClient_ListFailure(t *testing.T) {
	boom := stderrors.New("apiserver down")
	c := newClient(&stubExec{})
	c.k8s = fake.NewClientBuilder().WithScheme(scheme.Scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(context.Context, k8sclient.WithWatch, k8sclient.ObjectList, ...k8sclient.ListOption) error {
				return boom
			},
		}).Build()

	_, err := c.Status(t.Context())

	require.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, ErrPCSMNotReady)
	assert.Contains(t, err.Error(), "list pcsm pods")
}

func TestNotActive(t *testing.T) {
	tests := map[string]struct {
		stdout string
		stderr string
		want   bool
	}{
		"documented 409 body": {
			stdout: `{"ok":false,"error":"not_active","role":"STANDBY","message":"This instance is STANDBY. Active is running on pcsm-1:2242"}`,
			want:   true,
		},
		"plain-text standby message on stderr": {
			stderr: "Error: not_active: this instance is STANDBY",
			want:   true,
		},
		"successful status payload": {
			stdout: `{"state":"running","role":"ACTIVE"}`,
		},
		"unrelated failure": {
			stderr: "Error: connection refused",
		},
		"failed state is a real state, not a routing problem": {
			stdout: `{"state":"failed","error":"source unreachable"}`,
		},
		"a decoded body outranks the substring fallback": {
			stdout: `{"state":"failed","error":"collection myApp.not_active is missing"}`,
		},
		"empty output": {},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, notActive([]byte(tc.stdout), []byte(tc.stderr)))
		})
	}
}

func TestAnswered(t *testing.T) {
	tests := map[string]struct {
		stdout string
		want   bool
	}{
		"status payload":       {stdout: `{"state":"running","role":"ACTIVE"}`, want: true},
		"error payload":        {stdout: `{"ok":false,"error":"not_active"}`, want: true},
		"empty output":         {},
		"plain text":           {stdout: "Error: connection refused"},
		"json without a reply": {stdout: `{"role":"STANDBY"}`},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, answered([]byte(tc.stdout)))
		})
	}
}

func TestNew(t *testing.T) {
	cr := &psmdbv1.PerconaServerMongoDBClusterSync{Name: "cr", Namespace: "ns"}
	c := New(fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(), nil, cr)
	require.NotNil(t, c)
	assert.Same(t, cr, c.cr)
}

func TestTrimOutput(t *testing.T) {
	tests := map[string]struct {
		in   string
		want string
	}{
		"short string passes through": {in: "hello", want: "hello"},
		"empty string passes through": {in: "", want: ""},
		"at boundary passes through":  {in: strings.Repeat("x", 512), want: strings.Repeat("x", 512)},
		"oversize gets truncated":     {in: strings.Repeat("x", 600), want: strings.Repeat("x", 512) + "...(truncated)"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, trimOutput(tc.in))
		})
	}
}
