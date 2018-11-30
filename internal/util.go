package internal

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// ParseSpecResourceRequirements parses resource requirements to a corev1.ResourceList
func ParseSpecResourceRequirements(rsr *v1alpha1.ResourceSpecRequirements) (corev1.ResourceList, error) {
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

// GetContainerRunUID returns an int64-pointer reflecting the user ID a container
// should run as
func GetContainerRunUID(m *v1alpha1.PerconaServerMongoDB, serverVersion *v1alpha1.ServerVersion) *int64 {
	if GetPlatform(m, serverVersion) != v1alpha1.PlatformOpenshift {
		return &m.Spec.RunUID
	}
	return nil
}
