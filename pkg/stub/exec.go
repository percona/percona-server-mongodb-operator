package stub

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// printOutput outputs stdout/stderr log buffers from commands
func printOutputBuffer(r io.Reader, cmd, pod string) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fmt.Printf("%s (%s): %s\n", cmd, pod, strings.TrimSpace(scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		logrus.Errorf("Error printing output from %s (%s): %v", cmd, pod, err)
		return err
	}
	return nil
}

// stolen from https://github.com/saada/mongodb-operator/blob/master/pkg/stub/handler.go
// v2 of the api should have features for doing this, I would like to move to that later
//
// See: https://github.com/kubernetes/client-go/issues/45
//
func execCommandInContainer(pod corev1.Pod, containerName string, cmd []string) error {
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
		if port.Name == mongodPortName {
			containerPort = strconv.Itoa(int(port.ContainerPort))
		}
	}
	if containerPort == "" {
		return fmt.Errorf("cannot find mongod port with name: %s", mongodPortName)
	}

	req := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec")
	req.VersionedParams(&corev1.PodExecOptions{
		Container: containerName,
		Command:   cmd,
		Stdout:    true,
		Stderr:    true,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(cfg, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to run pod exec: %v", err)
	}

	var (
		stdOut bytes.Buffer
		stdErr bytes.Buffer
	)
	err = exec.Stream(remotecommand.StreamOptions{
		Stdout: &stdOut,
		Stderr: &stdErr,
	})

	logrus.WithFields(logrus.Fields{
		"pod":       pod.Name,
		"container": containerName,
		"command":   cmd[0],
	}).Info("running command in container")

	// print stdout
	logrus.Infof("%s stdout:", cmd[0])
	err = printOutputBuffer(&stdOut, cmd[0], pod.Name)
	if err != nil {
		return err
	}

	// print stderr, if exists
	if stdErr.Len() > 0 {
		logrus.Errorf("%s stderr:", cmd[0])
		err = printOutputBuffer(&stdErr, cmd[0], pod.Name)
		if err != nil {
			return err
		}
	}

	return nil
}
