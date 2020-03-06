package pbm

import (
	"context"
	"io/ioutil"
	"log"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	docker "github.com/docker/docker/client"
	"github.com/pkg/errors"
)

type Docker struct {
	cn  *docker.Client
	ctx context.Context
}

func NewDocker(ctx context.Context, host string) (*Docker, error) {
	cn, err := docker.NewClient(host, "1.39", nil, nil)
	if err != nil {
		return nil, errors.Wrap(err, "docker client")
	}

	return &Docker{
		cn:  cn,
		ctx: ctx,
	}, nil
}

// StopAgents stops agent containers of the given replicaset
func (d *Docker) StopAgents(rsName string) error {
	fltr := filters.NewArgs()
	fltr.Add("label", "com.percona.pbm.agent.rs="+rsName)
	containers, err := d.cn.ContainerList(d.ctx, types.ContainerListOptions{
		Filters: fltr,
	})
	if err != nil {
		return errors.Wrap(err, "container list")
	}
	if len(containers) == 0 {
		return errors.Errorf("no containers found for replset %s", rsName)
	}

	for _, c := range containers {
		log.Println("stopping container", c.ID)
		err = d.cn.ContainerStop(d.ctx, c.ID, nil)
		if err != nil {
			return errors.Wrapf(err, "stop container %s", c.ID)
		}
	}

	return nil
}

// StartAgents starts stopped agent containers of the given replicaset
func (d *Docker) StartAgents(rsName string) error {
	fltr := filters.NewArgs()
	fltr.Add("label", "com.percona.pbm.agent.rs="+rsName)
	containers, err := d.cn.ContainerList(d.ctx, types.ContainerListOptions{
		All:     true,
		Filters: fltr,
	})
	if err != nil {
		return errors.Wrap(err, "container list")
	}
	if len(containers) == 0 {
		return errors.Errorf("no containers found for replset %s", rsName)
	}

	for _, c := range containers {
		log.Println("Straing container", c.ID)
		err = d.cn.ContainerStart(d.ctx, c.ID, types.ContainerStartOptions{})
		if err != nil {
			return errors.Wrapf(err, "start container %s", c.ID)
		}
	}

	return nil
}

// RestartAgents restarts agent containers of the given replicaset
func (d *Docker) RestartAgents(rsName string) error {
	fltr := filters.NewArgs()
	fltr.Add("label", "com.percona.pbm.agent.rs="+rsName)
	containers, err := d.cn.ContainerList(d.ctx, types.ContainerListOptions{
		Filters: fltr,
	})
	if err != nil {
		return errors.Wrap(err, "container list")
	}

	for _, c := range containers {
		log.Println("restarting container", c.ID)
		err = d.cn.ContainerRestart(d.ctx, c.ID, nil)
		if err != nil {
			return errors.Wrapf(err, "restart container %s", c.ID)
		}
	}

	return nil
}

func (d *Docker) RunOnReplSet(rsName string, wait time.Duration, cmd ...string) error {
	fltr := filters.NewArgs()
	fltr.Add("label", "com.percona.pbm.agent.rs="+rsName)
	containers, err := d.cn.ContainerList(d.ctx, types.ContainerListOptions{
		Filters: fltr,
	})
	if err != nil {
		return errors.Wrap(err, "container list")
	}
	if len(containers) == 0 {
		return errors.Errorf("no containers found for replset %s", rsName)
	}

	var wg sync.WaitGroup
	for _, c := range containers {
		log.Printf("run %v on conainer %s%v\n", cmd, c.ID, c.Names)
		wg.Add(1)
		go func(container types.Container) {
			out, err := d.RunCmd(container.ID, wait, cmd...)
			if err != nil {
				log.Fatalf("ERROR: run cmd %v on container %s%v: %v", cmd, container.ID, container.Names, err)
			}
			if out != "" {
				log.Println(out)
			}
			wg.Done()
		}(c)
	}

	wg.Wait()
	return nil
}

func (d *Docker) RunCmd(containerID string, wait time.Duration, cmd ...string) (string, error) {
	execConf := types.ExecConfig{
		User:         "root",
		Cmd:          cmd,
		Privileged:   true,
		AttachStderr: true,
		AttachStdout: true,
	}
	id, err := d.cn.ContainerExecCreate(d.ctx, containerID, execConf)
	if err != nil {
		return "", errors.Wrap(err, "ContainerExecCreate")
	}

	container, err := d.cn.ContainerExecAttach(d.ctx, id.ID, execConf)
	if err != nil {
		return "", errors.Wrap(err, "attach to failed container")
	}
	defer container.Close()

	tmr := time.NewTimer(wait)
	tkr := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-tmr.C:
			return "", errors.New("timeout reached")
		case <-tkr.C:
			insp, err := d.cn.ContainerExecInspect(d.ctx, id.ID)
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
