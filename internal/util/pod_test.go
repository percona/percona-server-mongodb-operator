package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodList(t *testing.T) {
	podList := PodList()
	assert.Equal(t, "Pod", podList.TypeMeta.Kind)
}

func TestGetPodNames(t *testing.T) {
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: t.Name() + "-0",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: t.Name() + "-1",
			},
		},
	}
	podNames := GetPodNames(pods)
	assert.Len(t, podNames, 2)
	assert.Equal(t, []string{t.Name() + "-0", t.Name() + "-1"}, podNames)
}

func TestGetPodContainer(t *testing.T) {
	pod := corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: t.Name(),
				},
			},
		},
	}
	assert.NotNil(t, GetPodContainer(&pod, t.Name()))
	assert.Nil(t, GetPodContainer(&pod, "doesnt exist"))
}
