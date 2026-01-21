package perconaservermongodb

import (
	"bytes"
	"context"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

// PVCUsage contains information about PVC disk usage
type PVCUsage struct {
	PVCName      string
	UsedBytes    int64
	TotalBytes   int64
	UsagePercent int
}

func (r *ReconcilePerconaServerMongoDB) getPVCUsageFromMetrics(
	ctx context.Context,
	pod *corev1.Pod,
	pvcName string,
) (*PVCUsage, error) {
	// Execute df command in the mongod container to get disk usage
	// df -B1 /data/db outputs in bytes
	// Example output:
	// Filesystem       1B-blocks       Used   Available Use% Mounted on
	// /dev/sdb        3094126592  221798400  2855550976   8% /data/db
	var stdout, stderr bytes.Buffer
	command := []string{"df", "-B1", config.MongodContainerDataDir}

	err := r.clientcmd.Exec(ctx, pod, "mongod", command, nil, &stdout, &stderr, false)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to execute df in pod %s: %s", pod.Name, stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 {
		return nil, errors.Errorf("unexpected df output format: %s", stdout.String())
	}

	fields := strings.Fields(lines[1])
	if len(fields) < 6 {
		return nil, errors.Errorf("unexpected df output fields: %s", lines[1])
	}

	totalBytes, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse total bytes: %s", fields[1])
	}

	usedBytes, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse used bytes: %s", fields[2])
	}

	usagePercent := 0
	if totalBytes > 0 {
		usagePercent = int((usedBytes * 100) / totalBytes)
	}

	return &PVCUsage{
		PVCName:      pvcName,
		UsedBytes:    usedBytes,
		TotalBytes:   totalBytes,
		UsagePercent: usagePercent,
	}, nil
}
