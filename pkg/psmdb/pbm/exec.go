package pbm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/pkg/errors"
)

const BackupAgentContainerName = "backup-agent"

func (p *PBM) exec(ctx context.Context, command []string, stdin io.Reader, out any) error {
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	err := p.execClient.Exec(ctx, p.pod, p.containerName, command, stdin, &stdout, &stderr, false)
	if err != nil {
		return errors.Wrapf(err, "stdout: %s, stderr: %s", stdout.String(), stderr.String())
	}

	if out == nil {
		return nil
	}

	return json.Unmarshal(stdout.Bytes(), out)
}
