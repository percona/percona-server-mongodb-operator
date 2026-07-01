package client

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
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
	LagTimeSeconds int64  `json:"lagTimeSeconds,omitempty"`
}

type StartOptions struct {
	IncludeNamespaces []string
	ExcludeNamespaces []string
}

var ErrPCSMNotReady = stderrors.New("no ready PCSM pod")

type podExecer interface {
	Exec(ctx context.Context, pod *corev1.Pod, container string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error
}

type Client struct {
	k8s  k8sclient.Client
	exec podExecer
	cr   *psmdbv1.PerconaServerMongoDBClusterSync
}

func New(k8s k8sclient.Client, cc *clientcmd.Client, cr *psmdbv1.PerconaServerMongoDBClusterSync) *Client {
	return &Client{k8s: k8s, exec: cc, cr: cr}
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	raw, err := c.run(ctx, "status")
	if err != nil {
		return Status{}, err
	}
	var s Status
	if err := json.Unmarshal(raw, &s); err != nil {
		return Status{}, errors.Wrapf(err, "decode pcsm status output: %s", string(raw))
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
	_, err := c.run(ctx, args...)
	return err
}

func (c *Client) Pause(ctx context.Context) error {
	_, err := c.run(ctx, "pause")
	return err
}

func (c *Client) Resume(ctx context.Context, fromFailure bool) error {
	args := []string{"resume"}
	if fromFailure {
		args = append(args, "--from-failure")
	}
	_, err := c.run(ctx, args...)
	return err
}

func (c *Client) Finalize(ctx context.Context) error {
	_, err := c.run(ctx, "finalize")
	return err
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	pod, err := c.findReadyPod(ctx)
	if err != nil {
		return nil, err
	}

	cmd := append([]string{pcsmBinary}, args...)

	var stdout, stderr bytes.Buffer
	if execErr := c.exec.Exec(ctx, pod, clustersync.ContainerName, cmd, nil, &stdout, &stderr, false); execErr != nil {
		return nil, errors.Wrapf(execErr, "exec pcsm %s: %s", strings.Join(args, " "), trimOutput(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (c *Client) findReadyPod(ctx context.Context) (*corev1.Pod, error) {
	var pods corev1.PodList
	selector := k8sclient.MatchingLabels{
		naming.LabelKubernetesInstance:  c.cr.Name,
		naming.LabelKubernetesComponent: clustersync.ComponentPCSM,
	}
	if err := c.k8s.List(ctx, &pods, k8sclient.InNamespace(c.cr.Namespace), selector); err != nil {
		return nil, errors.Wrap(err, "list pcsm pods")
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.DeletionTimestamp != nil {
			continue
		}
		for _, cs := range p.Status.ContainerStatuses {
			if cs.Name == clustersync.ContainerName && cs.Ready {
				return p, nil
			}
		}
	}
	return nil, ErrPCSMNotReady
}

func trimOutput(s string) string {
	const maxLen = 512
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}
