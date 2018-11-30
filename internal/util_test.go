package internal

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestParseResourceRequirementsList(t *testing.T) {
	// test human cpu format
	parsed, err := parseResourceRequirementsList(&v1alpha1.ResourceSpecRequirements{
		Cpu:     "500m",
		Memory:  "0.5Gi",
		Storage: "5Gi",
	})
	assert.NoError(t, err)
	cpu := parsed[corev1.ResourceCPU]
	assert.Equal(t, "500m", cpu.String())

	// test float cpu format
	parsed, err = parseResourceRequirementsList(&v1alpha1.ResourceSpecRequirements{
		Cpu:     "1.0",
		Memory:  "0.5Gi",
		Storage: "5Gi",
	})
	assert.NoError(t, err)
	cpu = parsed[corev1.ResourceCPU]
	assert.Equal(t, "1", cpu.String())

	// test int cpu format
	parsed, err = parseResourceRequirementsList(&v1alpha1.ResourceSpecRequirements{
		Cpu:     "2",
		Memory:  "0.5Gi",
		Storage: "5Gi",
	})
	assert.NoError(t, err)
	cpu = parsed[corev1.ResourceCPU]
	assert.Equal(t, "2", cpu.String())

	// test bad cpu format
	_, err = parseResourceRequirementsList(&v1alpha1.ResourceSpecRequirements{
		Cpu: "& ^^^ %%",
	})
	assert.Error(t, err)

	// test bad cpu string format
	_, err = parseResourceRequirementsList(&v1alpha1.ResourceSpecRequirements{
		Cpu: "& ^^^ %%m",
	})
	assert.Error(t, err)

	// test bad memory format
	_, err = parseResourceRequirementsList(&v1alpha1.ResourceSpecRequirements{
		Memory: "& ! !!!!",
	})
	assert.Error(t, err)

	// test bad storage format
	_, err = parseResourceRequirementsList(&v1alpha1.ResourceSpecRequirements{
		Storage: "!!!&&&&",
	})
	assert.Error(t, err)
}

func TestParseReplsetResourceRequirements(t *testing.T) {
	replset := &v1alpha1.ReplsetSpec{
		ResourcesSpec: &v1alpha1.ResourcesSpec{
			Limits: &v1alpha1.ResourceSpecRequirements{
				Cpu:     "500m",
				Memory:  "0.5Gi",
				Storage: "5Gi",
			},
			Requests: &v1alpha1.ResourceSpecRequirements{
				Cpu:    "500m",
				Memory: "0.5Gi",
			},
		},
	}
	r, err := ParseReplsetResourceRequirements(replset)
	assert.NoError(t, err)
	assert.NotNil(t, r)
	assert.Len(t, r.Limits, 3)
	assert.Len(t, r.Requests, 2)

	// test 'requests' are only added when there are no 'limits' set
	// https://jira.percona.com/browse/CLOUD-44
	cpuLimits := r.Limits[corev1.ResourceCPU]
	cpuRequests := r.Limits[corev1.ResourceCPU]
	assert.Equal(t, "500m", cpuLimits.String())
	assert.Equal(t, "500m", cpuRequests.String())

	replset.ResourcesSpec.Limits.Cpu = ""
	r, err = ParseReplsetResourceRequirements(replset)
	assert.NoError(t, err)
	assert.NotNil(t, r)
	cpuLimits = r.Limits[corev1.ResourceCPU]
	cpuRequests = r.Requests[corev1.ResourceCPU]
	assert.True(t, cpuLimits.IsZero())
	assert.True(t, cpuRequests.IsZero())
}
