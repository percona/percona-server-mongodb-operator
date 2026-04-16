package pbm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/oplog"
)

type Ctl struct {
	cn        *client.Client
	ctx       context.Context
	container string
	env       []string
}

var backupNameRE = regexp.MustCompile(`Starting backup '([0-9\-\:TZ]+)'`)

func NewCtl(ctx context.Context, host, pbmContainer string) (*Ctl, error) {
	cn, err := client.NewClientWithOpts(client.WithHost(host))
	if err != nil {
		return nil, errors.Wrap(err, "docker client")
	}

	return &Ctl{
		cn:        cn,
		ctx:       ctx,
		container: pbmContainer,
	}, nil
}

func (c *Ctl) PITRon() error {
	out, err := c.RunCmd("pbm", "config", "--set", "pitr.enabled=true", "--wait")
	if err != nil {
		return errors.Wrap(err, "config set pitr.enabled=true")
	}

	_, err = c.RunCmd("pbm", "config", "--set", "pitr.oplogSpanMin=1", "--wait")
	if err != nil {
		return errors.Wrap(err, "config set pitr.oplogSpanMin=1")
	}

	fmt.Println("done", out)
	return nil
}

func (c *Ctl) PITRoff() error {
	out, err := c.RunCmd("pbm", "config", "--set", "pitr.enabled=false", "--wait")
	if err != nil {
		return err
	}

	fmt.Println("done", out)
	return nil
}

func (c *Ctl) ApplyConfig(file string) error {
	out, err := c.RunCmd("pbm", "config", "--file", file, "--wait")
	if err != nil {
		return err
	}

	fmt.Println("done", out)
	return nil
}

func (c *Ctl) Resync() error {
	out, err := c.RunCmd("pbm", "config", "--force-resync", "--wait")
	if err != nil {
		return err
	}

	fmt.Println("done", out)
	return nil
}

func (c *Ctl) Backup(typ defs.BackupType, opts ...string) (string, error) {
	cmd := append([]string{"pbm", "backup", "--type", string(typ), "--compression", "s2"}, opts...)
	out, err := c.RunCmd(cmd...)
	if err != nil {
		return "", err
	}

	fmt.Println("done", out)
	name := backupNameRE.FindStringSubmatch(out)
	if name == nil {
		return "", errors.Errorf("no backup name found in output:\n%s", out)
	}
	return name[1], nil
}

type ListOut struct {
	Snapshots []SnapshotStat `json:"snapshots"`
	PITR      struct {
		On       bool                   `json:"on"`
		Ranges   []PitrRange            `json:"ranges"`
		RsRanges map[string][]PitrRange `json:"rsRanges,omitempty"`
	} `json:"pitr"`
}

type SnapshotStat struct {
	Name       string      `json:"name"`
	Size       int64       `json:"size,omitempty"`
	Status     defs.Status `json:"status"`
	Err        string      `json:"error,omitempty"`
	RestoreTS  int64       `json:"restoreTo"`
	PBMVersion string      `json:"pbmVersion"`
}

type PitrRange struct {
	Err   string         `json:"error,omitempty"`
	Range oplog.Timeline `json:"range"`
}

func (c *Ctl) List() (*ListOut, error) {
	o, err := c.RunCmd("pbm", "list", "-o", "json")
	if err != nil {
		return nil, errors.Wrap(err, "run pbm list -o json")
	}

	l := &ListOut{}
	err = json.Unmarshal(skipCtl(o), l)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshal list")
	}

	return l, nil
}

func skipCtl(str string) []byte {
	for i := 0; i < len(str); i++ {
		if str[i] == '{' {
			return []byte(str[i:])
		}
	}
	return []byte(str)
}

func (c *Ctl) CheckRestore(bcpName string, waitFor time.Duration) error {
	type rlist struct {
		Start    int         `json:"Start"`
		Status   defs.Status `json:"Status"`
		Type     string      `json:"Type"`
		Name     string      `json:"Name"`
		Snapshot string      `json:"Snapshot"`
		Error    string      `json:"Error"`
	}
	tmr := time.NewTimer(waitFor)
	tkr := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-tmr.C:
			list, err := c.RunCmd("pbm", "list", "--restore")
			if err != nil {
				return errors.Wrap(err, "timeout reached. get restores list")
			}
			return errors.Errorf("timeout reached. restores list:\n%s", list)
		case <-tkr.C:
			out, err := c.RunCmd("pbm", "list", "--restore", "-o", "json")
			if err != nil {
				return err
			}

			// trim rubbish from the output
			i := strings.Index(out, "[")
			if i == -1 || len(out) <= i {
				continue
			}
			out = out[i:]

			var list []rlist
			err = json.Unmarshal([]byte(out), &list)
			if err != nil {
				log.Printf("\n\n%s\n\n", strings.TrimSpace(out))
				return errors.Wrap(err, "unmarshal list")
			}
			for _, r := range list {
				if r.Snapshot != bcpName {
					continue
				}

				switch r.Status {
				case defs.StatusDone:
					return nil
				case defs.StatusError:
					return errors.Errorf("failed with %s", r.Error)
				}
			}
		}
	}
}

func (c *Ctl) CheckPITRestore(t time.Time, timeout time.Duration) error {
	rinlist := "restore time: " + t.Format("2006-01-02T15:04:05Z")
	return c.waitForRestore(rinlist, timeout)
}

func (c *Ctl) CheckOplogReplay(a, b time.Time, timeout time.Duration) error {
	rinlist := fmt.Sprintf("Oplog Replay: %v - %v",
		a.UTC().Format(time.RFC3339),
		b.UTC().Format(time.RFC3339))
	return c.waitForRestore(rinlist, timeout)
}

func (c *Ctl) waitForRestore(rinlist string, waitFor time.Duration) error {
	tmr := time.NewTimer(waitFor)
	tkr := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-tmr.C:
			list, err := c.RunCmd("pbm", "list", "--restore")
			if err != nil {
				return errors.Wrap(err, "timeout reached. get backups list")
			}
			return errors.Errorf("timeout reached. backups list:\n%s", list)
		case <-tkr.C:
			out, err := c.RunCmd("pbm", "list", "--restore")
			if err != nil {
				return err
			}
			for _, s := range strings.Split(out, "\n") {
				if !strings.Contains(s, rinlist) {
					continue
				}

				_, status, _ := strings.Cut(s, "\t")
				status = strings.TrimSpace(status)
				if status == "done" {
					return nil
				}
				if strings.Contains(status, "Failed with") {
					return errors.New(status)
				}
			}
		}
	}
}

// Restore starts restore and returns the name of op
func (c *Ctl) Restore(bcpName string, options []string) (string, error) {
	command := append([]string{"pbm", "restore", bcpName, "-o", "json"}, options...)
	o, err := c.RunCmd(command...)
	if err != nil {
		return "", errors.Wrap(err, "run meta")
	}
	o = strings.TrimSpace(o)
	if i := strings.Index(o, "{"); i != -1 {
		o = o[i:]
	}
	m := struct {
		Name string `json:"name"`
	}{}
	err = json.Unmarshal([]byte(o), &m)
	if err != nil {
		return "", errors.Wrapf(err, "unmarshal restore meta \n%s\n", o)
	}
	return m.Name, nil
}

func (c *Ctl) ReplayOplog(a, b time.Time) error {
	_, err := c.RunCmd("pbm", "oplog-replay",
		"--start", a.Format("2006-01-02T15:04:05"),
		"--end", b.Format("2006-01-02T15:04:05"))
	return err
}

func (c *Ctl) PITRestore(t time.Time) error {
	_, err := c.RunCmd("pbm", "restore", "--time", t.Format("2006-01-02T15:04:05"))
	return err
}

func (c *Ctl) PITRestoreClusterTime(t, i uint32) error {
	_, err := c.RunCmd("pbm", "restore", "--time", fmt.Sprintf("%d,%d", t, i))
	return err
}

func (c *Ctl) RunCmd(cmds ...string) (string, error) {
	execConf := container.ExecOptions{
		Env:          c.env,
		Cmd:          cmds,
		AttachStderr: true,
		AttachStdout: true,
	}
	id, err := c.cn.ContainerExecCreate(c.ctx, c.container, execConf)
	if err != nil {
		return "", errors.Wrap(err, "ContainerExecCreate")
	}

	container, err := c.cn.ContainerExecAttach(c.ctx, id.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", errors.Wrap(err, "attach to failed container")
	}
	defer container.Close()

	tmr := time.NewTimer(time.Duration(float64(defs.WaitBackupStart) * 3))
	tkr := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-tmr.C:
			return "", errors.New("timeout reached")
		case <-tkr.C:
			insp, err := c.cn.ContainerExecInspect(c.ctx, id.ID)
			if err != nil {
				return "", errors.Wrap(err, "ContainerExecInspect")
			}
			if !insp.Running {
				logs, err := io.ReadAll(container.Reader)
				if err != nil {
					return "", errors.Wrap(err, "read logs of failed container")
				}

				switch insp.ExitCode {
				case 0:
					return string(logs), nil
				default:
					return "", errors.Errorf("container exited with %d code. Logs: %s", insp.ExitCode, logs)
				}
			}
		}
	}
}

func (c *Ctl) ContainerLogs() (string, error) {
	r, err := c.cn.ContainerLogs(
		c.ctx, c.container,
		container.LogsOptions{
			ShowStderr: true,
		})
	if err != nil {
		return "", errors.Wrap(err, "get logs of failed container")
	}
	defer r.Close()
	logs, err := io.ReadAll(r)
	if err != nil {
		return "", errors.Wrap(err, "read logs of failed container")
	}

	return string(logs), nil
}
