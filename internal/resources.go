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

// ParseReplsetResourceRequirements parses the resource section of the spec to a
// corev1.ResourceRequirements object
func ParseReplsetResourceRequirements(replset *v1alpha1.ReplsetSpec) (corev1.ResourceRequirements, error) {
	var err error
	rr := corev1.ResourceRequirements{
		Limits:   corev1.ResourceList{},
		Requests: corev1.ResourceList{},
	}

	rr.Limits, err = parseResourceRequirementsList(replset.Limits)
	if err != nil {
		return rr, err
	}

	// only set cpu+memory resource requests if limits are set
	// https://jira.percona.com/browse/CLOUD-44
	requests, err := parseResourceRequirementsList(replset.Requests)
	if err != nil {
		return rr, err
	}
	if _, ok := rr.Limits[corev1.ResourceCPU]; ok {
		rr.Requests[corev1.ResourceCPU] = requests[corev1.ResourceCPU]
	}
	if _, ok := rr.Limits[corev1.ResourceMemory]; ok {
		rr.Requests[corev1.ResourceMemory] = requests[corev1.ResourceMemory]
	}

	return rr, nil
}
