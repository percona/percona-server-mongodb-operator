package pbm

import (
	"context"
	"log"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	docker "github.com/docker/docker/client"
	"github.com/pkg/errors"
)

func ClockSkew(rsName, ts, dockerHost string) error {
	log.Printf("== Skew the clock for %s on the replicaset %s ", ts, rsName)

	cn, err := docker.NewClient(dockerHost, "1.39", nil, nil)
	if err != nil {
		return errors.Wrap(err, "docker client")
	}
	defer cn.Close()

	fltr := filters.NewArgs()
	fltr.Add("label", "com.percona.pbm.agent.rs="+rsName)
	containers, err := cn.ContainerList(context.Background(), types.ContainerListOptions{
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

		log.Printf("Removing container %s/%s\n", containerOld.ID, containerOld.Name)
		err = cn.ContainerRemove(context.Background(), c.ID, types.ContainerRemoveOptions{Force: true})
		if err != nil {
			return errors.Wrapf(err, "remove container %s", c.ID)
		}

		envs := append(containerOld.Config.Env, []string{
			`LD_PRELOAD=/usr/lib/x86_64-linux-gnu/faketime/libfaketime.so.1`,
			`FAKETIME=` + ts,
		}...)

		log.Printf("Creating container %s/%s with the clock skew %s\n", containerOld.ID, containerOld.Name, ts)
		containerNew, err := cn.ContainerCreate(context.Background(), &container.Config{
			Image:  containerOld.Image,
			Env:    envs,
			Cmd:    []string{"pbm-agent"},
			Labels: containerOld.Config.Labels,
		},
			containerOld.HostConfig,
			&network.NetworkingConfig{
				EndpointsConfig: containerOld.NetworkSettings.Networks,
			},
			containerOld.Name)
		if err != nil {
			return errors.Wrap(err, "ContainerCreate")
		}

		err = cn.ContainerStart(context.Background(), containerNew.ID, types.ContainerStartOptions{})
		if err != nil {
			return errors.Wrap(err, "ContainerStart")
		}
		log.Printf("New container %s/%s has started\n", containerOld.ID, containerOld.Name)
	}

	return nil
}
