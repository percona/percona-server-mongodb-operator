// Package fake provides a fake implementation of clientcmd.Client for use in tests.
package fake

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	restclient "k8s.io/client-go/rest"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
)

// Client is a fake clientcmd.Client. Set ExecFunc and RESTFunc to control
// behavior; unset functions fall back to no-op defaults (Exec returns nil,
// REST returns nil).
type Client struct {
	ExecFunc func(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error
	RESTFunc func() restclient.Interface
}

var _ clientcmd.Client = (*Client)(nil)

func New() clientcmd.Client {
	return new(Client)
}

func (c *Client) Exec(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
	if c.ExecFunc != nil {
		return c.ExecFunc(ctx, pod, containerName, command, stdin, stdout, stderr, tty)
	}
	return nil
}

func (c *Client) REST() restclient.Interface {
	if c.RESTFunc != nil {
		return c.RESTFunc()
	}
	return nil
}
