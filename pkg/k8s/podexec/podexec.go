package podexec

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

type client struct {
	client     corev1client.CoreV1Interface
	restconfig *restclient.Config
}

type Client interface {
	Exec(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer) error
}

func NewClient(config *restclient.Config) (Client, error) {
	cl, err := corev1client.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &client{
		client:     cl,
		restconfig: config,
	}, nil
}

func (c *client) Exec(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	req := c.client.RESTClient().
		Post().
		Namespace(pod.Namespace).
		Resource("pods").
		Name(pod.Name).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdin:     stdin != nil,
			Stdout:    stdout != nil,
			Stderr:    stderr != nil,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(c.restconfig, "POST", req.URL())
	if err != nil {
		return err
	}

	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	})
}
