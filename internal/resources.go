package internal

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// parseResourceRequirementsList parses resource requirements to a corev1.ResourceList
func parseResourceRequirementsList(rsr *v1alpha1.ResourceSpecRequirements) (corev1.ResourceList, error) {
	rl := corev1.ResourceList{}

	if rsr.Cpu != "" {
		cpu := rsr.Cpu
		if !strings.HasSuffix(cpu, "m") {
			cpuFloat64, err := strconv.ParseFloat(cpu, 64)
			if err != nil {
				return nil, err
			}
			cpu = fmt.Sprintf("%.1f", cpuFloat64)
		}
		cpuQuantity, err := resource.ParseQuantity(cpu)
		if err != nil {
			return nil, err
		}
		rl[corev1.ResourceCPU] = cpuQuantity
	}

	if rsr.Memory != "" {
		memoryQuantity, err := resource.ParseQuantity(rsr.Memory)
		if err != nil {
			return nil, err
		}
		rl[corev1.ResourceMemory] = memoryQuantity
	}

	if rsr.Storage != "" {
		storageQuantity, err := resource.ParseQuantity(rsr.Storage)
		if err != nil {
			return nil, err
		}
		rl[corev1.ResourceStorage] = storageQuantity
	}

	return rl, nil
}

// ParseResourceSpecRequirements parses the resource section of the spec to a
// corev1.ResourceRequirements object
func ParseResourceSpecRequirements(specLimits, specRequests *v1alpha1.ResourceSpecRequirements) (corev1.ResourceRequirements, error) {
	var rr corev1.ResourceRequirements

	if specLimits != nil {
		limits, err := parseResourceRequirementsList(specLimits)
		if err != nil {
			return rr, err
		}
		if len(limits) > 0 {
			rr.Limits = limits
		}
	}

	if specRequests != nil {
		requests, err := parseResourceRequirementsList(specRequests)
		if err != nil {
			return rr, err
		}
		if len(requests) > 0 {
			rr.Requests = requests
		}
	}

	return rr, nil
}
