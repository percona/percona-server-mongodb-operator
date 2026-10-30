package client

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"slices"
	"strings"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/clustersync"
)

const pcsmBinary = "pcsm"

type Status struct {
	State          string `json:"state"`
	Info           string `json:"info,omitempty"`
	Error          string `json:"error,omitempty"`
	Role           string `json:"role,omitempty"`
	LagTimeSeconds int64  `json:"lagTimeSeconds,omitempty"`
}

type StartOptions struct {
	IncludeNamespaces []string
	ExcludeNamespaces []string
}

var (
	ErrPCSMNotReady = stderrors.New("no ready PCSM pod")
	ErrNoActivePCSM = stderrors.New("no active PCSM instance")
)

// notActiveCode is the error code a STANDBY instance replies with; see
// https://docs.percona.com/percona-clustersync-for-mongodb/high-availability.html
const notActiveCode = "not_active"

type podExecer interface {
	Exec(ctx context.Context, pod *corev1.Pod, container string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error
}

type Client struct {
	k8s  k8sclient.Client
	exec podExecer
	cr   *psmdbv1.PerconaServerMongoDBClusterSync

	activePod string
}

func New(k8s k8sclient.Client, cc clientcmd.Client, cr *psmdbv1.PerconaServerMongoDBClusterSync) *Client {
	return &Client{k8s: k8s, exec: cc, cr: cr}
}

// WithActivePod seeds the last known active instance so the common case
// costs one exec instead of one per pod.
func (c *Client) WithActivePod(name string) *Client {
	c.activePod = name
	return c
}

// ActiveInstance reports the pod the last successful command ran on, or
// the seeded pod if nothing has run yet.
func (c *Client) ActiveInstance() string {
	return c.activePod
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	raw, runErr := c.run(ctx, true, "status")
	// pcsm status exits non-zero when state=failed but still prints a valid
	// JSON payload on stdout. Parse first so the reconciler sees the failed
	// state and can trigger `resume --from-failure`.
	var s Status
	if decodeErr := json.Unmarshal(raw, &s); decodeErr != nil {
		if runErr != nil {
			return Status{}, runErr
		}
		return Status{}, errors.Wrapf(decodeErr, "decode pcsm status output: %s", string(raw))
	}
	if s.State == "" && runErr != nil {
		return Status{}, runErr
	}
	return s, nil
}

func (c *Client) Start(ctx context.Context, opts StartOptions) error {
	args := []string{"start"}
	if len(opts.IncludeNamespaces) > 0 {
		args = append(args, "--include-namespaces="+strings.Join(opts.IncludeNamespaces, ","))
	}
	if len(opts.ExcludeNamespaces) > 0 {
		args = append(args, "--exclude-namespaces="+strings.Join(opts.ExcludeNamespaces, ","))
	}
	_, err := c.run(ctx, false, args...)
	return err
}

func (c *Client) Pause(ctx context.Context) error {
	_, err := c.run(ctx, false, "pause")
	return err
}

func (c *Client) Resume(ctx context.Context, fromFailure bool) error {
	args := []string{"resume"}
	if fromFailure {
		args = append(args, "--from-failure")
	}
	_, err := c.run(ctx, false, args...)
	return err
}

func (c *Client) Finalize(ctx context.Context) error {
	_, err := c.run(ctx, false, "finalize")
	return err
}

// run executes a pcsm command against the ACTIVE instance. A not_active
// reply advances to the next pod, as does a transport failure for readOnly
// commands; any other failure is the active instance's answer and is
// returned as-is, because retrying a start or finalize could run it twice.
func (c *Client) run(ctx context.Context, readOnly bool, args ...string) ([]byte, error) {
	pods, err := c.readyPods(ctx)
	if err != nil {
		return nil, err
	}

	cmd := append([]string{pcsmBinary}, args...)
	var transportErr error
	for _, pod := range pods {
		var stdout, stderr bytes.Buffer
		execErr := c.exec.Exec(ctx, pod, clustersync.ContainerName, cmd, nil, &stdout, &stderr, false)
		if execErr == nil {
			c.activePod = pod.Name
			return stdout.Bytes(), nil
		}
		if notActive(stdout.Bytes(), stderr.Bytes()) {
			continue
		}
		err := errors.Wrapf(execErr, "exec pcsm %s on %s: %s",
			strings.Join(args, " "), pod.Name, trimOutput(stderr.String()))
		// Without a reply the exec may never have reached PCSM.
		if !answered(stdout.Bytes()) {
			if transportErr == nil {
				transportErr = err
			}
			if readOnly {
				continue
			}
			return stdout.Bytes(), err
		}
		c.activePod = pod.Name
		return stdout.Bytes(), err
	}
	if transportErr != nil {
		return nil, transportErr
	}
	return nil, ErrNoActivePCSM
}

// readyPods returns the ready PCSM pods, last known active first, the
// rest sorted by name so iteration is deterministic.
func (c *Client) readyPods(ctx context.Context) ([]*corev1.Pod, error) {
	var pods corev1.PodList
	selector := k8sclient.MatchingLabels{
		naming.LabelKubernetesInstance:  c.cr.Name,
		naming.LabelKubernetesComponent: clustersync.ComponentPCSM,
	}
	if err := c.k8s.List(ctx, &pods, k8sclient.InNamespace(c.cr.Namespace), selector); err != nil {
		return nil, errors.Wrap(err, "list pcsm pods")
	}

	ready := make([]*corev1.Pod, 0, len(pods.Items))
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.DeletionTimestamp != nil {
			continue
		}
		for _, cs := range p.Status.ContainerStatuses {
			if cs.Name == clustersync.ContainerName && cs.Ready {
				ready = append(ready, p)
				break
			}
		}
	}
	if len(ready) == 0 {
		return nil, ErrPCSMNotReady
	}

	slices.SortFunc(ready, func(a, b *corev1.Pod) int {
		switch c.activePod {
		case a.Name:
			return -1
		case b.Name:
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return ready, nil
}

// notActive reports whether the output is a STANDBY instance refusing the
// command. PCSM answers with a JSON body; the substring fallback covers
// builds that only print the message.
func notActive(stdout, stderr []byte) bool {
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(stdout, &body); err == nil {
		return body.Error == notActiveCode
	}
	return strings.Contains(string(stderr), notActiveCode) || strings.Contains(string(stdout), notActiveCode)
}

// answered reports whether stdout holds a PCSM reply rather than nothing.
func answered(stdout []byte) bool {
	var body struct {
		State string `json:"state"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(stdout, &body); err != nil {
		return false
	}
	return body.State != "" || body.Error != ""
}

func trimOutput(s string) string {
	const maxLen = 512
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}
