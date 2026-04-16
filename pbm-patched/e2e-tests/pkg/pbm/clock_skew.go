package pbm

import (
	"context"
	"log"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

func ClockSkew(rsName, ts, dockerHost string) error {
	if ts != "0" {
		log.Printf("==Skew the clock for %s on the replicaset %s ", ts, rsName)
	}

	cn, err := client.NewClientWithOpts(client.WithHost(dockerHost))
	if err != nil {
		return errors.Wrap(err, "docker client")
	}
	defer cn.Close()

	fltr := filters.NewArgs()
	fltr.Add("label", "com.percona.pbm.agent.rs="+rsName)
	containers, err := cn.ContainerList(context.Background(), container.ListOptions{
		Filters: fltr,
	})
	if err != nil {
		return errors.Wrap(err, "container list")
	}

	for _, c := range containers {
		containerOld, err := cn.ContainerInspect(context.Background(), c.ID)
		if err != nil {
			return errors.Wrapf(err, "ContainerInspect for %s", c.ID)
		}

		envs := append([]string{}, containerOld.Config.Env...)
		if ts == "0" {
			ldPreloadDefined := false
			for _, env := range envs {
				if strings.HasPrefix(env, "LD_PRELOAD") {
					ldPreloadDefined = true
					break
				}
			}

			if !ldPreloadDefined {
				log.Printf("Variable LD_PRELOAD isn't defined, skipping container restart for %s\n", containerOld.Name)
				continue
			}

			var filteredEnvs []string
			for _, env := range envs {
				if !strings.HasPrefix(env, "LD_PRELOAD") {
					filteredEnvs = append(filteredEnvs, env)
				}
			}
			envs = filteredEnvs
		} else {
			envs = append(envs,
				`LD_PRELOAD=/lib64/faketime/libfaketime.so.1`,
				`FAKETIME=`+ts,
			)
		}

		log.Printf("Removing container %s/%s\n", containerOld.ID, containerOld.Name)
		err = cn.ContainerRemove(context.Background(), c.ID, container.RemoveOptions{Force: true})
		if err != nil {
			return errors.Wrapf(err, "remove container %s", c.ID)
		}

		log.Printf("Creating container %s/%s with the clock skew %s\n", containerOld.ID, containerOld.Name, ts)
		containerNew, err := cn.ContainerCreate(context.Background(), &container.Config{
			Image:  containerOld.Image,
			Env:    envs,
			Cmd:    []string{"pbm-agent"},
			Labels: containerOld.Config.Labels,
			User:   "1001",
		},
			containerOld.HostConfig,
			&network.NetworkingConfig{
				EndpointsConfig: containerOld.NetworkSettings.Networks,
			},
			nil,
			containerOld.Name)
		if err != nil {
			return errors.Wrap(err, "ContainerCreate")
		}

		err = cn.ContainerStart(context.Background(), containerNew.ID, container.StartOptions{})
		if err != nil {
			return errors.Wrap(err, "ContainerStart")
		}
		log.Printf("New container %s/%s has started\n", containerOld.ID, containerOld.Name)
	}

	return nil
}
