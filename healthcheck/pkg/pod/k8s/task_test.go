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
	"testing"

	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg/pod"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPkgPodK8STask(t *testing.T) {
	assert.Implements(t, (*pod.Task)(nil), &Task{})

	task := NewTask(
		DefaultNamespace,
		&CustomResourceState{
			Name: pkg.DefaultServiceName,
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: t.Name(),
			},
			Status: corev1.PodStatus{},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Env:   []corev1.EnvVar{},
						Ports: []corev1.ContainerPort{},
					},
				},
			},
		},
	)

	assert.NotNil(t, task)
	assert.Equal(t, t.Name(), task.Name())

	// task types
	task.pod.Spec.Containers[0].Name = ""
	assert.False(t, task.IsTaskType(pod.TaskTypeMongod))
	task.pod.Spec.Containers[0].Name = mongodContainerName
	assert.True(t, task.IsTaskType(pod.TaskTypeMongod))
	assert.False(t, task.IsTaskType(pod.TaskTypeMongos))
	task.pod.Spec.Containers[0].Name = mongodBackupContainerName
	assert.True(t, task.IsTaskType(pod.TaskTypeMongodBackup))
	assert.False(t, task.IsTaskType(pod.TaskTypeMongod))

	// test empty state
	assert.False(t, task.HasState())

	// test non-running state
	task.pod.Status.Phase = corev1.PodPending
	assert.True(t, task.HasState())
	assert.False(t, task.IsRunning())

	// test running state
	task.pod.Status.Phase = corev1.PodRunning
	assert.True(t, task.IsRunning())
	assert.Equal(t, "RUNNING", task.State().String())

	// test empty replset name
	_, err := task.GetMongoReplsetName()
	assert.Error(t, err)

	// test set replset name
	task.pod.Spec.Containers[0].Env = []corev1.EnvVar{
		{
			Name:  pkg.EnvMongoDBReplset,
			Value: "rs",
		},
	}
	rsName, err := task.GetMongoReplsetName()
	assert.NoError(t, err)
	assert.Equal(t, "rs", rsName)

	// test GetMongoHost()
	assert.Equal(t,
		t.Name()+"."+pkg.DefaultServiceName+"-"+rsName+"."+DefaultNamespace+"."+clusterServiceDNSSuffix,
		GetMongoHost(t.Name(), pkg.DefaultServiceName, rsName, DefaultNamespace),
	)

	// empty mongo addr
	_, err = task.GetMongoAddr()
	assert.Error(t, err)

	// set mongo addr
	task.pod.Spec.Containers[0].Ports = []corev1.ContainerPort{{
		Name:          "mongodb",
		HostPort:      int32(27017),
		ContainerPort: int32(27018),
	}}
	addr, err := task.GetMongoAddr()
	assert.NoError(t, err)
	assert.Equal(t, t.Name()+"."+pkg.DefaultServiceName+"-"+rsName+"."+DefaultNamespace+"."+clusterServiceDNSSuffix, addr.Host)
	assert.Equal(t, 27017, addr.Port)
	task.pod.Spec.Containers[0].Ports[0].HostPort = int32(0)
	addr, err = task.GetMongoAddr()
	assert.NoError(t, err)
	assert.Equal(t, 27018, addr.Port)

	// test .IsUpdating() is false
	task.cr.Statefulsets = []appsv1.StatefulSet{
		{
			Spec: appsv1.StatefulSetSpec{
				ServiceName: pkg.DefaultServiceName + "-rs",
			},
			Status: appsv1.StatefulSetStatus{
				CurrentRevision: "abc123",
				UpdateRevision:  "abc123",
				ReadyReplicas:   int32(3),
				CurrentReplicas: int32(3),
			},
		},
	}
	assert.False(t, task.IsUpdating())

	// test .IsUpdating() is true if revisions are different
	task.cr.Statefulsets = []appsv1.StatefulSet{
		{
			Spec: appsv1.StatefulSetSpec{
				ServiceName: pkg.DefaultServiceName + "-rs",
			},
			Status: appsv1.StatefulSetStatus{
				CurrentRevision: "abc123",
				UpdateRevision:  "abc123456",
				ReadyReplicas:   int32(3),
				CurrentReplicas: int32(3),
			},
		},
	}
	assert.True(t, task.IsUpdating())

	// test .IsUpdating() is true if replica #s are different
	task.cr.Statefulsets = []appsv1.StatefulSet{
		{
			Spec: appsv1.StatefulSetSpec{
				ServiceName: pkg.DefaultServiceName + "-rs",
			},
			Status: appsv1.StatefulSetStatus{
				CurrentRevision: "abc123",
				UpdateRevision:  "abc123",
				ReadyReplicas:   int32(2),
				CurrentReplicas: int32(3),
			},
		},
	}
	assert.True(t, task.IsUpdating())
}
