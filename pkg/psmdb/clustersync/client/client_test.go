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

func pod(name string, instance string, ready bool) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "ns",
			Labels: map[string]string{
				naming.LabelKubernetesInstance:  instance,
				naming.LabelKubernetesComponent: clustersync.ComponentPCSM,
			},
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
		cr:   &psmdbv1.PerconaServerMongoDBClusterSync{ObjectMeta: metav1.ObjectMeta{Name: "cr", Namespace: "ns"}},
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
			wantErr: "exec pcsm pause: Error: connection refused: exit status 1",
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
		stdout  []byte
		want    Status
		wantErr string
	}{
		"replicating decodes all fields": {
			stdout: []byte(`{"state":"replicating","lagTimeSeconds":3,"info":"ok"}`),
			want:   Status{State: "replicating", LagTimeSeconds: 3, Info: "ok"},
		},
		"failed surfaces error field": {
			stdout: []byte(`{"state":"failed","error":"source unreachable"}`),
			want:   Status{State: "failed", Error: "source unreachable"},
		},
		"bad JSON wraps decode error": {
			stdout:  []byte(`not-json`),
			wantErr: "decode pcsm status output",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stub := &stubExec{stdout: tc.stdout}
			got, err := newClient(stub, readyPod()).Status(t.Context())

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

func TestClient_FindReadyPod(t *testing.T) {
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

func TestNew(t *testing.T) {
	cr := &psmdbv1.PerconaServerMongoDBClusterSync{ObjectMeta: metav1.ObjectMeta{Name: "cr", Namespace: "ns"}}
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
