package pbm

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	corev1 "k8s.io/api/core/v1"
)

type Snapshot struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Status     string `json:"status"`
	RestoreTo  int64  `json:"restoreTo"`
	PBMVersion string `json:"pbmVersion"`
	Type       string `json:"type"`
	Source     string `json:"src"`
}

type PITRChunk struct {
	Range struct {
		Start int64 `json:"start"`
		End   int64 `json:"end"`
	} `json:"range"`
}

type Backups struct {
	Type       string     `json:"type"`
	Path       string     `json:"path"`
	Region     string     `json:"region"`
	Snapshots  []Snapshot `json:"snapshot"`
	PITRChunks struct {
		Size   int64       `json:"size"`
		Chunks []PITRChunk `json:"pitrChunks"`
	} `json:"pitrChunks"`
}

type Node struct {
	Host  string `json:"host"`
	Agent string `json:"agent"`
	Role  string `json:"role"`
	OK    bool   `json:"ok"`
}

type Cluster struct {
	ReplSet string `json:"rs"`
	Nodes   []Node `json:"nodes"`
}

type Running struct {
	OpID    string `json:"opId"`
	Status  string `json:"status"`
	StartTS int64  `json:"startTS"`
	Name    string `json:"name"`
	Type    string `json:"type"`
}

type Status struct {
	Backups Backups `json:"backups"`
	Cluster Cluster `json:"cluster"`
	PITR    struct {
		Conf bool `json:"conf"`
		Run  bool `json:"run"`
	} `json:"pitr"`
	Running Running `json:"running"`
}

func GetStatus(ctx context.Context, cli *clientcmd.Client, pod *corev1.Pod) (Status, error) {
	status := Status{}

	stdout := bytes.Buffer{}
	stderr := bytes.Buffer{}

	err := exec(ctx, cli, pod, []string{"pbm", "status", "-o", "json"}, &stdout, &stderr)
	if err != nil {
		return status, err
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
