package stub

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestGetContainer(t *testing.T) {
	pod := corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: t.Name(),
				},
			},
		},
	}
	assert.NotNil(t, getContainer(pod, t.Name()))
	assert.Nil(t, getContainer(pod, "doesnt exist"))
}

func TestGetMongodPort(t *testing.T) {
	assert.Equal(t, "27017", getMongodPort(&corev1.Container{
		Ports: []corev1.ContainerPort{
			{
				Name:          mongodPortName,
				ContainerPort: int32(27017),
			},
		},
	}))
	assert.Equal(t, "", getMongodPort(&corev1.Container{
		Ports: []corev1.ContainerPort{},
	}))
}
