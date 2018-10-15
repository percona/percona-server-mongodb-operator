package stub

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// stolen from https://github.com/saada/mongodb-operator/blob/master/pkg/stub/handler.go
// v2 of the api should have features for doing this, I would like to move to that later
//
// See: https://github.com/kubernetes/client-go/issues/45
//
func execMongoCommandsInContainer(pod corev1.Pod, containerName string, mongoCmds []string) error {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %v", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %v", err)
	}

	// find the mongod container
	var container *corev1.Container
	for _, cont := range pod.Spec.Containers {
		if cont.Name == containerName {
			container = &cont
			break
		}
	}
	if container == nil {
		return nil
	}

	// find the mongod port
	var containerPort string
	for _, port := range container.Ports {
		if port.Name == "mongodb" {
			containerPort = strconv.Itoa(int(port.ContainerPort))
		}
	}
	if containerPort == "" {
		return fmt.Errorf("cannot find mongod port")
	}

	req := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec")
	req.VersionedParams(&corev1.PodExecOptions{
		Container: containerName,
		Command: []string{
			"/usr/bin/mongo",
			"--port=" + containerPort,
			"--quiet",
			"admin",
		},
		Stdout: true,
		Stderr: true,
		Stdin:  true,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to run pod exec: %v", err)
	}

	var (
		stdOut bytes.Buffer
		stdErr bytes.Buffer
		stdIn  bytes.Buffer
	)
	mongoCmd := strings.Join(mongoCmds, "; ")
	_, err = stdIn.WriteString(mongoCmd)
	if err != nil {
		return err
	}

	err = exec.Stream(remotecommand.StreamOptions{
		Stdout: &stdOut,
		Stderr: &stdErr,
		Stdin:  &stdIn,
	})

	logrus.WithFields(logrus.Fields{
		"pod":       pod.Name,
		"container": containerName,
	}).Infof("running mongo command '%s'", mongoCmd)

	logrus.Infof("mongostdout: %s", strings.TrimSpace(stdOut.String()))
	if stdErr.Len() > 0 {
		logrus.Errorf("mongo stderr: %s", strings.TrimSpace(stdErr.String()))
	}

	if err != nil {
		return fmt.Errorf("could not execute: %v", err)
	}

	return nil
}
