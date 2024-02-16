package pbm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	corev1 "k8s.io/api/core/v1"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
)

type BackupType string

const (
	BackupTypeLogical  BackupType = "logical"
	BackupTypePhysical BackupType = "physical"
)

type BackupOptions struct {
	Type             BackupType `json:"type"`
	Compression      string     `json:"compression"`
	CompressionLevel string     `json:"compressionLevel"`
	Namespace        string     `json:"namespace"`
}

type BackupResponse struct {
	Name    string `json:"name"`
	Storage string `json:"storage"`
	Error   string `json:"Error"`
}

func RunBackup(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, opts BackupOptions) (BackupResponse, error) {
	response := BackupResponse{}

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{
		"pbm", "backup",
		"--ns=" + opts.Namespace,
		"--compression=" + opts.Compression,
		"--compression-level=" + opts.CompressionLevel,
		"--out=json",
	}

	err := exec(ctx, cli, pod, cmd, &stdout, &stderr)
	if err != nil {
		return response, err
	}

	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return response, err
	}

	if len(response.Error) > 0 {
		return response, errors.New(response.Error)
	}

	return response, nil
}
