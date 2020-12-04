// Copyright 2018 Percona LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package k8s

import (
	"os"
	"testing"

	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg/pod"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestInternalPodK8SPods(t *testing.T) {
	assert.Implements(t, (*pod.Source)(nil), &Pods{})

	p := NewPods(DefaultNamespace)
	assert.NotNil(t, p)

	pods, err := p.Pods()
	assert.NoError(t, err)
	assert.Len(t, pods, 0)

	corev1Pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: "not-" + t.Name(),
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: t.Name(),
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Env: []corev1.EnvVar{
							{
								Name:  pkg.EnvMongoDBReplset,
								Value: "testRs",
							},
							{
								Name:  pkg.EnvMongoDBPort,
								Value: t.Name(),
							},
						},
						Ports: []corev1.ContainerPort{
							{
								Name:     mongodbPortName,
								HostIP:   "1.2.3.4",
								HostPort: int32(27017),
							},
						},
					},
				},
			},
		},
	}
	statefulsets := []appsv1.StatefulSet{
		{
			Spec: appsv1.StatefulSetSpec{
				ServiceName: pkg.DefaultServiceName + "-testRs",
			},
		},
	}

	p.Update(&CustomResourceState{
		Name:         "test-cluster",
		Pods:         corev1Pods,
		Statefulsets: statefulsets,
	})
	pods, _ = p.Pods()
	assert.Len(t, pods, 1)
	assert.Equal(t, t.Name(), pods[0])

	// test .GetTasks()
	tasks, err := p.GetTasks(t.Name())
	assert.NoError(t, err)
	assert.Len(t, tasks, 1)

	// test several PSMDB CRs
	// https://jira.percona.com/browse/CLOUD-76
	p2 := NewPods(DefaultNamespace)
	p2.Update(&CustomResourceState{
		Name: "test-cluster1",
		Pods: []corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: t.Name() + "-1",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Env: []corev1.EnvVar{
								{
									Name:  pkg.EnvMongoDBReplset,
									Value: "rs",
								},
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          mongodbPortName,
									ContainerPort: int32(27017),
								},
							},
						},
					},
				},
			},
		},
	})
	p2.Update(&CustomResourceState{
		Name: "test-cluster2",
		Pods: []corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: t.Name() + "-2",
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
				},
			},
		},
	})
	pods, _ = p2.Pods()
	assert.Len(t, pods, 2, "expected 2 pods (1 pod per updated CR)")
	tasks, err = p2.GetTasks(t.Name() + "-1")
	assert.NoError(t, err)
	assert.Len(t, tasks, 1)
	assert.Equal(t, t.Name()+"-1", tasks[0].Name())

	// test .GetMongoAddr() is correct for 1 multi-CR task
	// https://jira.percona.com/browse/CLOUD-76
	addr, err := tasks[0].GetMongoAddr()
	assert.NoError(t, err)
	assert.NotNil(t, addr)
	assert.Equal(t, "TestInternalPodK8SPods-1.test-cluster1-rs.psmdb.svc.cluster.local:27017", addr.String())

	// test .Delete() of CR
	p2.Delete(&CustomResourceState{
		Name: "test-cluster2",
	})
	pods, _ = p2.Pods()
	assert.Len(t, pods, 1, "expected 1 pods after delete of 2nd CR")

	// test Succeeded pod is not listed by .Pods()
	corev1Pods[1].Status.Phase = corev1.PodSucceeded
	p.Update(&CustomResourceState{
		Name: "test-cluster",
		Pods: corev1Pods,
	})
	pods, _ = p.Pods()
	assert.Len(t, pods, 0)

	assert.Equal(t, "k8s", p.Name())

	assert.Equal(t, "", p.URL())
	os.Setenv(EnvKubernetesHost, t.Name())
	os.Setenv(EnvKubernetesPort, "443")
	defer os.Unsetenv(EnvKubernetesHost)
	defer os.Unsetenv(EnvKubernetesPort)
	assert.Equal(t, "tcp://"+t.Name()+":443", p.URL())
}
