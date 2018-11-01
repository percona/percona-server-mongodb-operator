package stub

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodList(t *testing.T) {
	podList := podList()
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
	podNames := getPodNames(pods)
	assert.Len(t, podNames, 2)
	assert.Equal(t, []string{t.Name() + "-0", t.Name() + "-1"}, podNames)
}
