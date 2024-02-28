package pbm

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
)

const BackupAgentContainerName = "backup-agent"

func exec(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, container string, command []string, stdout, stderr io.Writer) error {
	return cli.Exec(ctx, pod, container, command, nil, stdout, stderr, false)
}
