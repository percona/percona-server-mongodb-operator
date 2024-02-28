package pbm

import (
	"context"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
)

const BackupAgentContainerName = "backup-agent"

func exec(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, container string, command []string, stdout, stderr io.Writer) error {
	l := log.FromContext(ctx)

	l.V(1).Info("Executing PBM command", "command", strings.Join(command, " "), "pod", pod.Name, "namespace", pod.Namespace)

	return cli.Exec(ctx, pod, container, command, nil, stdout, stderr, false)
}
