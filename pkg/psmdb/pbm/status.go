package pbm

import (
	"context"
	"time"
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
	Host   string   `json:"host"`
	Agent  string   `json:"agent"`
	Role   string   `json:"role"`
	OK     bool     `json:"ok"`
	Errors []string `json:"errors"`
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
	Backups Backups   `json:"backups"`
	Cluster []Cluster `json:"cluster"`
	PITR    struct {
		Conf  bool   `json:"conf"`
		Run   bool   `json:"run"`
		Error string `json:"error"`
	} `json:"pitr"`
	Running Running `json:"running"`
}

// GetStatus returns the status of PBM
func (p *PBM) GetStatus(ctx context.Context) (Status, error) {
	status := Status{}

	cmd := []string{p.pbmPath, "status", "-o", "json"}

	err := p.exec(ctx, cmd, nil, &status)
	if err != nil {
		return status, wrapExecError(err, cmd)
	}

	return status, nil
}

func (p *PBM) GetRunningOperation(ctx context.Context) (Running, error) {
	running := Running{}

	status, err := p.GetStatus(ctx)
	if err != nil {
		return running, err
	}

	return status.Running, nil
}

// HasRunningOperation checks if there is a running operation in PBM
func (p *PBM) HasRunningOperation(ctx context.Context) (bool, error) {
	status, err := p.GetStatus(ctx)
	if err != nil {
		if IsNotConfigured(err) {
			return false, nil
		}
		return false, err
	}

	return status.Running.Status != "", nil
}

// IsPITRRunning checks if PITR is running or enabled in config
func (p *PBM) IsPITRRunning(ctx context.Context) (bool, error) {
	status, err := p.GetStatus(ctx)
	if err != nil {
		return false, err
	}

	return status.PITR.Run || status.PITR.Conf, nil
}

func (p *PBM) LatestPITRChunk(ctx context.Context) (string, error) {
	status, err := p.GetStatus(ctx)
	if err != nil {
		return "", err
	}

	latest := status.Backups.PITRChunks.Chunks[len(status.Backups.PITRChunks.Chunks)-1].Range.End
	ts := time.Unix(int64(latest), 0).UTC()

	return ts.Format("2006-01-02T15:04:05"), nil
}
