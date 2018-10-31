package stub

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

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

func TestParseSpecResourceRequirements(t *testing.T) {
	// test human cpu format
	parsed, err := parseSpecResourceRequirements(&v1alpha1.ResourceSpecRequirements{
		Cpu:     "500m",
		Memory:  "0.5Gi",
		Storage: "5Gi",
	})
	assert.NoError(t, err)
	cpu := parsed[corev1.ResourceCPU]
	assert.Equal(t, "500m", cpu.String())

	// test float cpu format
	parsed, err = parseSpecResourceRequirements(&v1alpha1.ResourceSpecRequirements{
		Cpu:     "1.0",
		Memory:  "0.5Gi",
		Storage: "5Gi",
	})
	assert.NoError(t, err)
	cpu = parsed[corev1.ResourceCPU]
	assert.Equal(t, "1", cpu.String())

	// test int cpu format
	parsed, err = parseSpecResourceRequirements(&v1alpha1.ResourceSpecRequirements{
		Cpu:     "2",
		Memory:  "0.5Gi",
		Storage: "5Gi",
	})
	assert.NoError(t, err)
	cpu = parsed[corev1.ResourceCPU]
	assert.Equal(t, "2", cpu.String())

	// test bad cpu format
	_, err = parseSpecResourceRequirements(&v1alpha1.ResourceSpecRequirements{
		Cpu: "& ^^^ %%",
	})
	assert.Error(t, err)

	// test bad memory format
	_, err = parseSpecResourceRequirements(&v1alpha1.ResourceSpecRequirements{
		Memory: "& ! !!!!",
	})
	assert.Error(t, err)

	// test bad storage format
	_, err = parseSpecResourceRequirements(&v1alpha1.ResourceSpecRequirements{
		Storage: "!!!&&&&",
	})
	assert.Error(t, err)
}
