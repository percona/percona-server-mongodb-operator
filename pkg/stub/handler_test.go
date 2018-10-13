package stub

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestGetMongoURI(t *testing.T) {
	spec := corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name: mongodContainerName,
				Ports: []corev1.ContainerPort{
					{
						Name:     "mongodb",
						HostPort: int32(27017),
					},
					{
						Name:     "not-mongodb",
						HostPort: int32(99999),
					},
				},
			},
			{
				Name: "not-mongod",
			},
		},
	}
	pods := []corev1.Pod{
		{
			Spec: spec,
			Status: corev1.PodStatus{
				HostIP: "1.2.3.4",
			},
		},
		{
			Spec: spec,
			Status: corev1.PodStatus{
				HostIP: "1.2.3.5",
			},
		},
		{
			Spec: spec,
			Status: corev1.PodStatus{
				HostIP: "1.2.3.6",
			},
		},
	}
	assert.Equal(t, "mongodb://1.2.3.4:27017,1.2.3.5:27017,1.2.3.6:27017", getMongoURI(pods, "mongodb"))
	assert.Equal(t, "", getMongoURI(pods, "doesntexist"))
	assert.Equal(t, "", getMongoURI([]corev1.Pod{}, "mongodb"))
}
