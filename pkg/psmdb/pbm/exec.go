package pbm

import (
	"context"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
)

const pbmSidecarName = "backup-agent"

func exec(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, command []string, stdout, stderr io.Writer) error {
	l := log.FromContext(ctx)

	l.V(1).Info("Executing PBM command", "command", strings.Join(command, " "), "pod", pod.Name, "namespace", pod.Namespace)

	return cli.Exec(ctx, pod, pbmSidecarName, command, nil, stdout, stderr, false)
}
