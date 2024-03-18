package pbm

import (
	"context"

	"github.com/pkg/errors"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

type RestoreOptions struct {
	BackupName       string `json:"backupName"`
	BaseSnapshot     string `json:"baseSnapshot"`
	ReplsetRemapping string `json:"replsetRemapping"`
	Namespace        string `json:"namespace"`
	Time             string `json:"time"`
}

type RestoreResponse struct {
	Name  string `json:"name"`
	Error string `json:"Error"`
}

type DescribeRestoreOptions struct {
	Name       string `json:"name"`
	ConfigPath string `json:"configPath"`
}

type DescribeRestoreResponse struct {
	Name               string          `json:"name"`
	OpID               string          `json:"opid"`
	Backup             string          `json:"backup"`
	Type               defs.BackupType `json:"type"`
	Status             defs.Status     `json:"status"`
	LastTransitionTS   int64           `json:"last_transition_ts"`
	LastTransitionTime string          `json:"last_transition_time"`
	Replsets           []struct {
		Name               string      `json:"name"`
		Status             defs.Status `json:"status"`
		LastTransitionTS   int64       `json:"last_transition_ts"`
		LastTransitionTime string      `json:"last_transition_time"`
		Error              string      `json:"error"`
	} `json:"replsets"`
	Error string `json:"Error"`
}

func (p *PBM) RunRestore(ctx context.Context, opts RestoreOptions) (RestoreResponse, error) {
	response := RestoreResponse{}

	cmd := []string{p.pbmPath, "restore", opts.BackupName, "--out=json"}

	if len(opts.Namespace) > 0 {
		cmd = append(cmd, "--ns="+opts.Namespace)
	}

	if len(opts.ReplsetRemapping) > 0 {
		cmd = append(cmd, "--replset-remapping="+opts.ReplsetRemapping)
	}

	if len(opts.BaseSnapshot) > 0 {
		cmd = append(cmd, "--base-snapshot="+opts.BaseSnapshot)
	}

	if len(opts.Time) > 0 {
		cmd = append(cmd, "--time="+opts.Time)
	}

	err := p.exec(ctx, cmd, nil, &response)
	if err != nil {
		return response, wrapExecError(err, cmd)
	}

	if len(response.Error) > 0 {
		return response, errors.New(response.Error)
	}

	return response, nil
}

func (p *PBM) DescribeRestore(ctx context.Context, opts DescribeRestoreOptions) (DescribeRestoreResponse, error) {
	response := DescribeRestoreResponse{}

	cmd := []string{p.pbmPath, "describe-restore", opts.Name, "--out=json"}

	if len(opts.ConfigPath) > 0 {
		cmd = append(cmd, "--config="+opts.ConfigPath)
	}

	err := p.exec(ctx, cmd, nil, &response)
	if err != nil {
		return response, wrapExecError(err, cmd)
	}

	if len(response.Error) > 0 {
		return response, errors.New(response.Error)
	}

	return response, nil
}
