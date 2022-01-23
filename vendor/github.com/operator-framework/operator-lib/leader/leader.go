// Copyright 2020 The Operator-SDK Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package leader

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/operator-framework/operator-lib/internal/utils"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// ErrNoNamespace indicates that a namespace could not be found for the current
// environment
var ErrNoNamespace = utils.ErrNoNamespace

// podNameEnvVar is the constant for env variable POD_NAME
// which is the name of the current pod.
const podNameEnvVar = "POD_NAME"

var readNamespace = utils.GetOperatorNamespace

var log = logf.Log.WithName("leader")

// maxBackoffInterval defines the maximum amount of time to wait between
// attempts to become the leader.
const maxBackoffInterval = time.Second * 16

// Option is a function that can modify Become's Config
type Option func(*Config) error

// Config defines the configuration for Become
type Config struct {
	Client crclient.Client
}

func (c *Config) setDefaults() error {
	if c.Client == nil {
		config, err := config.GetConfig()
		if err != nil {
			return err
		}

		client, err := crclient.New(config, crclient.Options{})
		if err != nil {
			return err
		}
		c.Client = client
	}
	return nil
}

// WithClient returns an Option that sets the Client used by Become
func WithClient(cl crclient.Client) Option {
	return func(c *Config) error {
		c.Client = cl
		return nil
	}
}

// Become ensures that the current pod is the leader within its namespace. If
// run outside a cluster, it will skip leader election and return nil. It
// continuously tries to create a ConfigMap with the provided name and the
// current pod set as the owner reference. Only one can exist at a time with
// the same name, so the pod that successfully creates the ConfigMap is the
// leader. Upon termination of that pod, the garbage collector will delete the
// ConfigMap, enabling a different pod to become the leader.
func Become(ctx context.Context, lockName string, opts ...Option) error {
	log.Info("Trying to become the leader.")

	config := Config{}

	for _, opt := range opts {
		if err := opt(&config); err != nil {
			return err
		}
	}

	if err := config.setDefaults(); err != nil {
		return err
	}

	ns, err := readNamespace()
	if err != nil {
		return err
	}

	owner, err := myOwnerRef(ctx, config.Client, ns)
	if err != nil {
		return err
	}

	// check for existing lock from this pod, in case we got restarted
	existing := &corev1.ConfigMap{}
	key := crclient.ObjectKey{Namespace: ns, Name: lockName}
	err = config.Client.Get(ctx, key, existing)

	switch {
	case err == nil:
		for _, existingOwner := range existing.GetOwnerReferences() {
			if existingOwner.Name == owner.Name {
				log.Info("Found existing lock with my name. I was likely restarted.")
				log.Info("Continuing as the leader.")
				return nil
			}
			log.Info("Found existing lock", "LockOwner", existingOwner.Name)
		}
	case apierrors.IsNotFound(err):
		log.Info("No pre-existing lock was found.")
	default:
		log.Error(err, "Unknown error trying to get ConfigMap")
		return err
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            lockName,
			Namespace:       ns,
			OwnerReferences: []metav1.OwnerReference{*owner},
		},
	}

	// try to create a lock
	backoff := time.Second
	for {
		err := config.Client.Create(ctx, cm)
		switch {
		case err == nil:
			log.Info("Became the leader.")
			return nil
		case apierrors.IsAlreadyExists(err):
			// refresh the lock so we use current leader
			key := crclient.ObjectKey{Namespace: ns, Name: lockName}
			if err := config.Client.Get(ctx, key, existing); err != nil {
				log.Info("Leader lock configmap not found.")
				continue // configmap got lost ... just wait a bit
			}

			existingOwners := existing.GetOwnerReferences()
			switch {
			case len(existingOwners) != 1:
				log.Info("Leader lock configmap must have exactly one owner reference.", "ConfigMap", existing)
			case existingOwners[0].Kind != "Pod":
				log.Info("Leader lock configmap owner reference must be a pod.", "OwnerReference", existingOwners[0])
			default:
				leaderPod := &corev1.Pod{}
				key = crclient.ObjectKey{Namespace: ns, Name: existingOwners[0].Name}
				err = config.Client.Get(ctx, key, leaderPod)
				switch {
				case apierrors.IsNotFound(err):
					log.Info("Leader pod has been deleted, waiting for garbage collection to remove the lock.")
				case err != nil:
					return err
				case isPodEvicted(*leaderPod) && leaderPod.GetDeletionTimestamp() == nil:
					log.Info("Operator pod with leader lock has been evicted.", "leader", leaderPod.Name)
					log.Info("Deleting evicted leader.")
					// Pod may not delete immediately, continue with backoff
					err := config.Client.Delete(ctx, leaderPod)
					if err != nil {
						log.Error(err, "Leader pod could not be deleted.")
					}
				case isNotReadyNode(ctx, config.Client, leaderPod.Spec.NodeName):
					log.Info("the status of the node where operator pod with leader lock was running has been 'notReady'")
					log.Info("Deleting the leader.")

					//Mark the termainating status to the leaderPod and Delete the configmap lock
					if err := deleteLeader(ctx, config.Client, leaderPod, existing); err != nil {
						return err
					}

				default:
					log.Info("Not the leader. Waiting.")
				}
			}

			select {
			case <-time.After(wait.Jitter(backoff, .2)):
				if backoff < maxBackoffInterval {
					backoff *= 2
				}
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		default:
			log.Error(err, "Unknown error creating ConfigMap")
			return err
		}
	}
}

// myOwnerRef returns an OwnerReference that corresponds to the pod in which
// this code is currently running.
// It expects the environment variable POD_NAME to be set by the downwards API
func myOwnerRef(ctx context.Context, client crclient.Client, ns string) (*metav1.OwnerReference, error) {
	myPod, err := getPod(ctx, client, ns)
	if err != nil {
		return nil, err
	}

	owner := &metav1.OwnerReference{
		APIVersion: "v1",
		Kind:       "Pod",
		Name:       myPod.ObjectMeta.Name,
		UID:        myPod.ObjectMeta.UID,
	}
	return owner, nil
}

func isPodEvicted(pod corev1.Pod) bool {
	podFailed := pod.Status.Phase == corev1.PodFailed
	podEvicted := pod.Status.Reason == "Evicted"
	return podFailed && podEvicted
}

// getPod returns a Pod object that corresponds to the pod in which the code
// is currently running.
// It expects the environment variable POD_NAME to be set by the downwards API.
func getPod(ctx context.Context, client crclient.Client, ns string) (*corev1.Pod, error) {
	podName := os.Getenv(podNameEnvVar)
	if podName == "" {
		return nil, fmt.Errorf("required env %s not set, please configure downward API", podNameEnvVar)
	}

	log.V(1).Info("Found podname", "Pod.Name", podName)

	pod := &corev1.Pod{}
	key := crclient.ObjectKey{Namespace: ns, Name: podName}
	err := client.Get(ctx, key, pod)
	if err != nil {
		log.Error(err, "Failed to get Pod", "Pod.Namespace", ns, "Pod.Name", podName)
		return nil, err
	}

	// .Get() clears the APIVersion and Kind,
	// so we need to set them before returning the object.
	pod.TypeMeta.APIVersion = "v1"
	pod.TypeMeta.Kind = "Pod"

	log.V(1).Info("Found Pod", "Pod.Namespace", ns, "Pod.Name", pod.Name)

	return pod, nil
}

func getNode(ctx context.Context, client crclient.Client, nodeName string, node *corev1.Node) error {
	key := crclient.ObjectKey{Namespace: "", Name: nodeName}
	err := client.Get(ctx, key, node)
	if err != nil {
		log.Error(err, "Failed to get Node", "Node.Name", nodeName)
		return err
	}
	return nil
}

func isNotReadyNode(ctx context.Context, client crclient.Client, nodeName string) bool {
	leaderNode := &corev1.Node{}
	if err := getNode(ctx, client, nodeName, leaderNode); err != nil {
		return false
	}
	for _, condition := range leaderNode.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status != corev1.ConditionTrue {
			return true
		}
	}
	return false

}

func deleteLeader(ctx context.Context, client crclient.Client, leaderPod *corev1.Pod, existing *corev1.ConfigMap) error {
	err := client.Delete(ctx, leaderPod)
	if err != nil {
		log.Error(err, "Leader pod could not be deleted.")
		return err
	}
	err = client.Delete(ctx, existing)
	switch {
	case apierrors.IsNotFound(err):
		log.Info("ConfigMap has been deleted by prior operator.")
		return err
	case err != nil:
		return err
	}
	return nil
}
