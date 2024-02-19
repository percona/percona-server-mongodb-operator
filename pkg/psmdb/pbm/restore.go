package pbm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	corev1 "k8s.io/api/core/v1"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-server-mongodb-operator/clientcmd"
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

func RunRestore(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, opts RestoreOptions) (RestoreResponse, error) {
	response := RestoreResponse{}

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"pbm", "restore", opts.BackupName, "--out=json"}

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

func DescribeRestore(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, opts DescribeRestoreOptions) (DescribeRestoreResponse, error) {
	response := DescribeRestoreResponse{}

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"pbm", "describe-restore", opts.Name, "--out=json"}

	if len(opts.ConfigPath) > 0 {
		cmd = append(cmd, "--config="+opts.ConfigPath)
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
