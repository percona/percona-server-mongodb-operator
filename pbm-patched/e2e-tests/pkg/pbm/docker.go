package pbm

import (
	"context"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

type Docker struct {
	cn  *client.Client
	ctx context.Context
}

func NewDocker(ctx context.Context, host string) (*Docker, error) {
	cn, err := client.NewClientWithOpts(client.WithHost(host))
	if err != nil {
		return nil, errors.Wrap(err, "docker client")
	}

	return &Docker{
		cn:  cn,
		ctx: ctx,
	}, nil
}

// StopContainers stops containers with the given labels
func (d *Docker) StopContainers(labels []string) error {
	fltr := filters.NewArgs()
	for _, v := range labels {
		fltr.Add("label", v)
	}
	containers, err := d.cn.ContainerList(d.ctx, container.ListOptions{
		Filters: fltr,
	})
	if err != nil {
		return errors.Wrap(err, "container list")
	}

	for _, c := range containers {
		log.Println("stopping container", c.ID)
		err = d.cn.ContainerStop(d.ctx, c.ID, container.StopOptions{})
		if err != nil {
			return errors.Wrapf(err, "stop container %s", c.ID)
		}
	}

	return nil
}

// StopAgents stops agent containers of the given replicaset
func (d *Docker) StopAgents(rsName string) error {
	return d.StopContainers([]string{"com.percona.pbm.agent.rs=" + rsName})
}

// PauseAgents pause agent containers of the given replicaset
func (d *Docker) PauseAgents(rsName string) error {
	fltr := filters.NewArgs()
	fltr.Add("label", "com.percona.pbm.agent.rs="+rsName)
	containers, err := d.cn.ContainerList(d.ctx, container.ListOptions{
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
		err = d.cn.ContainerPause(d.ctx, c.ID)
		if err != nil {
			return errors.Wrapf(err, "stop container %s", c.ID)
		}
	}

	return nil
}

// UnpauseAgents unpause agent containers of the given replicaset
func (d *Docker) UnpauseAgents(rsName string) error {
	fltr := filters.NewArgs()
	fltr.Add("label", "com.percona.pbm.agent.rs="+rsName)
	containers, err := d.cn.ContainerList(d.ctx, container.ListOptions{
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
		err = d.cn.ContainerUnpause(d.ctx, c.ID)
		if err != nil {
			return errors.Wrapf(err, "stop container %s", c.ID)
		}
	}

	return nil
}

// StartAgents starts stopped agent containers of the given replicaset
func (d *Docker) StartAgents(rsName string) error {
	return d.StartContainers([]string{"com.percona.pbm.agent.rs=" + rsName})
}

// StartAgents starts stopped agent containers of the given replicaset
func (d *Docker) StartContainers(labels []string) error {
	fltr := filters.NewArgs()
	for _, v := range labels {
		fltr.Add("label", v)
	}
	containers, err := d.cn.ContainerList(d.ctx, container.ListOptions{
		All:     true,
		Filters: fltr,
	})
	if err != nil {
		return errors.Wrap(err, "container list")
	}
	if len(containers) == 0 {
		return errors.Errorf("no containers found for lables %v", labels)
	}

	for _, c := range containers {
		log.Println("Straing container", c.ID)
		err = d.cn.ContainerStart(d.ctx, c.ID, container.StartOptions{})
		if err != nil {
			return errors.Wrapf(err, "start container %s", c.ID)
		}
	}

	return nil
}

// RestartAgents restarts agent containers of the given replicaset
func (d *Docker) RestartAgents(rsName string) error {
	return d.RestartContainers([]string{"com.percona.pbm.agent.rs=" + rsName})
}

// RestartAgents restarts agent containers of the given replicaset
func (d *Docker) RestartContainers(labels []string) error {
	fltr := filters.NewArgs()
	for _, v := range labels {
		fltr.Add("label", v)
	}
	containers, err := d.cn.ContainerList(d.ctx, container.ListOptions{
		Filters: fltr,
	})
	if err != nil {
		return errors.Wrap(err, "container list")
	}

	for _, c := range containers {
		log.Println("restarting container", c.ID)
		err = d.cn.ContainerRestart(d.ctx, c.ID, container.StopOptions{})
		if err != nil {
			return errors.Wrapf(err, "restart container %s", c.ID)
		}
	}

	return nil
}

func (d *Docker) RunOnReplSet(rsName string, wait time.Duration, cmd ...string) error {
	fltr := filters.NewArgs()
	fltr.Add("label", "com.percona.pbm.agent.rs="+rsName)
	containers, err := d.cn.ContainerList(d.ctx, container.ListOptions{
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
	execConf := container.ExecOptions{
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

	container, err := d.cn.ContainerExecAttach(d.ctx, id.ID, container.ExecStartOptions{})
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

func (d *Docker) StartAgentContainers(labels []string) error {
	fltr := filters.NewArgs()
	for _, v := range labels {
		fltr.Add("label", v)
	}

	containers, err := d.cn.ContainerList(d.ctx, container.ListOptions{
		All:     true,
		Filters: fltr,
	})
	if err != nil {
		return errors.Wrap(err, "container list")
	}
	if len(containers) == 0 {
		return errors.Errorf("no containers found for labels %v", labels)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(containers))

	for _, c := range containers {
		wg.Add(1)
		go func(cont types.Container) {
			defer wg.Done()

			var buf strings.Builder
			var started bool
			for i := 1; i <= 5; i++ {
				err := d.cn.ContainerStart(d.ctx, cont.ID, container.StartOptions{})
				if err != nil {
					errCh <- errors.Wrapf(err, "start container %s", cont.ID)
					return
				}

				since := time.Now().Format(time.RFC3339Nano)
				time.Sleep(5 * time.Second)
				out, err := d.cn.ContainerLogs(d.ctx, cont.ID, container.LogsOptions{
					ShowStdout: true,
					ShowStderr: true,
					Follow:     false,
					Since:      since,
				})
				if err != nil {
					errCh <- errors.Wrapf(err, "get logs for container %s", cont.ID)
					return
				}

				buf.Reset()
				_, err = io.Copy(&buf, out)
				if err != nil {
					errCh <- errors.Wrapf(err, "read logs for container %s", cont.ID)
					return
				}

				if strings.Contains(buf.String(), "listening for the commands") {
					log.Printf("PBM agent %s started properly \n", cont.ID)
					started = true
					break
				}

				err = d.cn.ContainerStop(d.ctx, cont.ID, container.StopOptions{})
				if err != nil {
					errCh <- errors.Wrapf(err, "stop container %s", cont.ID)
					return
				}

				log.Printf("PBM agent %s wasn't started, retrying in %d seconds\n", cont.ID, i*5)
				time.Sleep(time.Duration(i*5) * time.Second)
			}

			if !started {
				errCh <- errors.Errorf("Can't start container %s, last logs: %s", cont.ID, buf.String())
			}
		}(c)
	}

	wg.Wait()
	close(errCh)

	errs := []error{}
	for err := range errCh {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Errorf("Can't start PBM agents:\n%s", errs)
	}

	return nil
}
