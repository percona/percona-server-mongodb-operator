package pbm

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"

	"github.com/pkg/errors"

	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

type BackupOptions struct {
	Type             defs.BackupType          `json:"type"`
	Compression      compress.CompressionType `json:"compression"`
	CompressionLevel *int                     `json:"compressionLevel"`
	Namespace        string                   `json:"namespace"`
}

type BackupResponse struct {
	Name    string `json:"name"`
	Storage string `json:"storage"`
	Error   string `json:"Error"`
}

type DescribeBackupOptions struct {
	Name            string `json:"name"`
	WithCollections bool   `json:"withCollections"`
}

type DescribeBackupResponse struct {
	Name               string          `json:"name"`
	OpId               string          `json:"opid"`
	Type               defs.BackupType `json:"type"`
	LastWriteTS        int64           `json:"last_write_ts"`
	LastTransitionTS   int64           `json:"last_transition_ts"`
	LastWriteTime      string          `json:"last_write_time"`
	LastTransitionTime string          `json:"last_transition_time"`
	MongoDBVersion     string          `json:"mongodb_version"`
	FCV                string          `json:"fcv"`
	PBMVersion         string          `json:"pbm_version"`
	Status             defs.Status     `json:"status"`
	Size               int64           `json:"size"`
	SizeH              string          `json:"size_h"`
	Replsets           []struct {
		Name               string      `json:"name"`
		Status             defs.Status `json:"status"`
		Node               string      `json:"node"`
		LastWriteTS        int64       `json:"last_write_ts"`
		LastTransitionTS   int64       `json:"last_transition_ts"`
		LastWriteTime      string      `json:"last_write_time"`
		LastTransitionTime string      `json:"last_transition_time"`
		ConfigSVR          bool        `json:"configsvr"`
		Collections        []string    `json:"collections"`
		Error              string      `json:"error"`
	} `json:"replsets"`
	Error string `json:"Error"`
}

func (p *PBM) RunBackup(ctx context.Context, opts BackupOptions) (BackupResponse, error) {
	response := BackupResponse{}

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{
		p.pbmPath, "backup",
		"--out=json",
		"--compression=" + string(opts.Compression),
	}

	if opts.Type != "" {
		cmd = append(cmd, "--type="+string(opts.Type))
	}

	if opts.Namespace != "" {
		cmd = append(cmd, "--ns="+opts.Namespace)
	}

	if opts.CompressionLevel != nil {
		cmd = append(cmd, "--compression-level="+strconv.Itoa(*opts.CompressionLevel))
	}

	err := p.exec(ctx, cmd, nil, &stdout, &stderr)
	if err != nil {
		return response, errors.Wrapf(err, "stdout: %s, stderr: %s", stdout.String(), stderr.String())
	}

	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return response, err
	}

	if len(response.Error) > 0 {
		return response, errors.New(response.Error)
	}

	return response, nil
}

func (p *PBM) DescribeBackup(ctx context.Context, opts DescribeBackupOptions) (DescribeBackupResponse, error) {
	response := DescribeBackupResponse{}

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{p.pbmPath, "describe-backup", opts.Name, "--out=json"}

	if opts.WithCollections {
		cmd = append(cmd, "--with-collections")
	}

	err := p.exec(ctx, cmd, nil, &stdout, &stderr)
	if err != nil {
		return response, errors.Wrapf(err, "stdout: %s, stderr: %s", stdout.String(), stderr.String())
	}

	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return response, err
	}

	if len(response.Error) > 0 {
		return response, errors.New(response.Error)
	}

	return response, nil
}

func (p *PBM) DeleteBackup(ctx context.Context, name string) error {
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"pbm", "delete-backup", name, "--yes"}

	err := p.exec(ctx, cmd, nil, &stdout, &stderr)
	if err != nil {
		return errors.Wrapf(err, "stdout: %s, stderr: %s", stdout.String(), stderr.String())
	}

	return nil
}
