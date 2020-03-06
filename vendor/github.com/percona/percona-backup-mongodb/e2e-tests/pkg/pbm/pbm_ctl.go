package pbm

import (
	"context"
	"fmt"
	"io/ioutil"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	docker "github.com/docker/docker/client"
	"github.com/pkg/errors"
)

type Ctl struct {
	cn        *docker.Client
	ctx       context.Context
	container string
	env       []string
}

var backupNameRE = regexp.MustCompile(`Backup '([0-9\-\:TZ]+)' to remote store`)

func NewCtl(ctx context.Context, host string) (*Ctl, error) {
	cn, err := docker.NewClient(host, "1.39", nil, nil)
	if err != nil {
		return nil, errors.Wrap(err, "docker client")
	}

	return &Ctl{
		cn:        cn,
		ctx:       ctx,
		container: "pbmagent_rs101",
	}, nil
}

func (c *Ctl) ApplyConfig(file string) error {
	out, err := c.RunCmd("pbm", "config", "--file", file)
	if err != nil {
		return err
	}

	fmt.Println("done", out)
	return nil
}

func (c *Ctl) Backup() (string, error) {
	out, err := c.RunCmd("pbm", "backup")
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

func (c *Ctl) CheckBackup(bcpName string, waitFor time.Duration) error {
	tmr := time.NewTimer(waitFor)
	tkr := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-tmr.C:
			list, err := c.RunCmd("pbm", "list")
			if err != nil {
				return errors.Wrap(err, "timeout reached. get backups list")
			}
			return errors.Errorf("timeout reached. backups list:\n%s", list)
		case <-tkr.C:
			out, err := c.RunCmd("pbm", "list")
			if err != nil {
				return err
			}
			for _, s := range strings.Split(out, "\n") {
				s := strings.TrimSpace(s)
				if s == bcpName {
					return nil
				}
				if strings.HasPrefix(s, bcpName) {
					status := strings.TrimSpace(strings.Split(s, bcpName)[1])
					if strings.Contains(status, "Failed with") {
						return errors.New(status)
					}
				}
			}
		}
	}
}

func (c *Ctl) CheckRestore(bcpName string, waitFor time.Duration) error {
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
				s := strings.TrimSpace(s)
				if s == bcpName {
					return nil
				}
				if strings.HasPrefix(s, bcpName) {
					status := strings.TrimSpace(strings.Split(s, bcpName)[1])
					if strings.Contains(status, "Failed with") {
						return errors.New(status)
					}
				}
			}
		}
	}
}

func (c *Ctl) Restore(bcpName string) error {
	_, err := c.RunCmd("pbm", "restore", bcpName)
	return err
}

func (c *Ctl) RunCmd(cmds ...string) (string, error) {
	execConf := types.ExecConfig{
		Env:          c.env,
		Cmd:          cmds,
		AttachStderr: true,
		AttachStdout: true,
	}
	id, err := c.cn.ContainerExecCreate(c.ctx, c.container, execConf)
	if err != nil {
		return "", errors.Wrap(err, "ContainerExecCreate")
	}

	container, err := c.cn.ContainerExecAttach(c.ctx, id.ID, execConf)
	if err != nil {
		return "", errors.Wrap(err, "attach to failed container")
	}
	defer container.Close()

	tmr := time.NewTimer(17 * time.Second)
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
				logs, err := ioutil.ReadAll(container.Reader)
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
		types.ContainerLogsOptions{
			ShowStderr: true,
		})
	if err != nil {
		return "", errors.Wrap(err, "get logs of failed container")
	}
	defer r.Close()
	logs, err := ioutil.ReadAll(r)
	if err != nil {
		return "", errors.Wrap(err, "read logs of failed container")
	}

	return string(logs), nil
}
