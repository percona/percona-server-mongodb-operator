package pbm

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
)

const pbmSidecarName = "backup-agent"

func exec(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, command []string, stdout, stderr io.Writer) error {
	return cli.Exec(ctx, pod, pbmSidecarName, command, nil, stdout, stderr, false)
}
