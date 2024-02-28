package physical

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/pbm"
)

func exec(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, container string, command []string, stdout, stderr *bytes.Buffer) error {
	l := log.FromContext(ctx)

	l.V(1).Info("Executing PBM command", "command", strings.Join(command, " "), "pod", pod.Name, "namespace", pod.Namespace)

	return cli.Exec(ctx, pod, container, command, nil, stdout, stderr, false)
}

func SetConfigFile(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, path string) error {
	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"/opt/percona/pbm", "config", "--file", path}

	err := exec(ctx, cli, pod, "mongod", cmd, &stdout, &stderr)
	if err != nil {
		return errors.Wrapf(err, "stdout: %s, stderr: %s", stdout.String(), stderr.String())
	}

	return nil
}

func GetStatus(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod) (pbm.Status, error) {
	status := pbm.Status{}

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"/opt/percona/pbm", "status", "-o", "json"}

	err := exec(ctx, cli, pod, "mongod", cmd, &stdout, &stderr)
	if err != nil {
		return status, errors.Wrapf(err, "stdout: %s stderr: %s", stdout.String(), stderr.String())
	}

	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		return status, err
	}

	return status, nil
}

func HasRunningOperation(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod) (bool, error) {
	status, err := GetStatus(ctx, cli, pod)
	if err != nil {
		return false, err
	}

	return status.Running.Status != "", nil
}

func RunRestore(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, opts pbm.RestoreOptions) (pbm.RestoreResponse, error) {
	response := pbm.RestoreResponse{}

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"/opt/percona/pbm", "restore", opts.BackupName, "--out=json"}

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

	err := exec(ctx, cli, pod, "mongod", cmd, &stdout, &stderr)
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

func DescribeRestore(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod, opts pbm.DescribeRestoreOptions) (pbm.DescribeRestoreResponse, error) {
	response := pbm.DescribeRestoreResponse{}

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	cmd := []string{"/opt/percona/pbm", "describe-restore", opts.Name, "--out=json"}

	if len(opts.ConfigPath) > 0 {
		cmd = append(cmd, "--config="+opts.ConfigPath)
	}

	err := exec(ctx, cli, pod, "mongod", cmd, &stdout, &stderr)
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
