// Package fake provides a fake implementation of podexec.Client for use in tests.
package fake

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"

	"github.com/percona/percona-server-mongodb-operator/pkg/k8s/podexec"
)

// Client is a fake podexec.Client. Set ExecFunc to control behavior;
// when unset, Exec returns nil.
type Client struct {
	ExecFunc func(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer) error
}

var _ podexec.Client = (*Client)(nil)

func New() podexec.Client {
	return new(Client)
}

func (c *Client) Exec(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if c.ExecFunc != nil {
		return c.ExecFunc(ctx, pod, containerName, command, stdin, stdout, stderr)
	}
	return nil
}
