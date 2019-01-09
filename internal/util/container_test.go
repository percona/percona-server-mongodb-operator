package util

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHasContainerTerminated(t *testing.T) {
	podStatus := corev1.PodStatus{
		ContainerStatuses: []corev1.ContainerStatus{
			{
				Name: t.Name(),
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode:   int32(0),
						FinishedAt: metav1.NewTime(time.Now()),
					},
				},
			},
		},
	}

	// test terminated container
	terminated, err := HasContainerTerminated(&podStatus, t.Name())
	assert.NoError(t, err)
	assert.True(t, terminated)

	// test non-terminated
	podStatus.ContainerStatuses[0].State.Terminated = nil
	terminated, err = HasContainerTerminated(&podStatus, t.Name())
	assert.NoError(t, err)
	assert.False(t, terminated)

	// test missing container
	_, err = HasContainerTerminated(&podStatus, "doesntexit")
	assert.Error(t, err)
}
