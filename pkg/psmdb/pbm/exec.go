package pbm

import (
	"context"
	"io"
)

const BackupAgentContainerName = "backup-agent"

func (p *PBM) exec(ctx context.Context, command []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return p.execClient.Exec(ctx, p.pod, p.containerName, command, stdin, stdout, stderr, false)
}
