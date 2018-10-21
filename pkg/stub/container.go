package stub

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	k8sPod "github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	gigaByte                 int64   = 1024 * 1024 * 1024
	minWiredTigerCacheSizeGB float64 = 0.25
)

// getContainer returns a container, if it exists
func getContainer(pod corev1.Pod, containerName string) *corev1.Container {
	for _, cont := range pod.Spec.Containers {
		if cont.Name == containerName {
			return &cont
		}
	}
	return nil
}

// getMongodPort returns the mongod port number as a string
func getMongodPort(container *corev1.Container) string {
	for _, port := range container.Ports {
		if port.Name == mongodPortName {
			return strconv.Itoa(int(port.ContainerPort))
		}
	}
	return ""
}

// The WiredTiger internal cache, by default, will use the larger of either 50% of
// (RAM - 1 GB), or 256 MB. For example, on a system with a total of 4GB of RAM the
// WiredTiger cache will use 1.5GB of RAM (0.5 * (4 GB - 1 GB) = 1.5 GB).
//
// https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.engineConfig.cacheSizeGB
//
func getWiredTigerCacheSizeGB(maxMemory *resource.Quantity, cacheRatio float64) float64 {
	size := math.Floor(cacheRatio * float64(maxMemory.Value()-gigaByte))
	sizeGB := size / float64(gigaByte)
	if sizeGB < minWiredTigerCacheSizeGB {
		sizeGB = minWiredTigerCacheSizeGB
	}
	return sizeGB
}

func newPSMDBContainerEnv(m *v1alpha1.PerconaServerMongoDB) []corev1.EnvVar {
	mSpec := m.Spec.MongoDB
	return []corev1.EnvVar{
		{
			Name:  motPkg.EnvServiceName,
			Value: m.Name,
		},
		{
			Name:  k8sPod.EnvNamespace,
			Value: m.Namespace,
		},
		{
			Name:  motPkg.EnvMongoDBPort,
			Value: strconv.Itoa(int(mSpec.Port)),
		},
		{
			Name:  motPkg.EnvMongoDBReplset,
			Value: mSpec.ReplsetName,
		},
	}
}

func newPSMDBInitContainer(m *v1alpha1.PerconaServerMongoDB) corev1.Container {
	// download mongodb-healthcheck, copy internal auth key and setup ownership+permissions
	cmds := []string{
		"wget -P /mongodb " + mongodbHealthcheckUrl,
		"wget -P /mongodb " + mongodbInitiatorUrl,
		"chmod +x /mongodb/mongodb-healthcheck /mongodb/k8s-mongodb-initiator",
		"cp " + mongoDBSecretsDir + "/" + mongoDBKeySecretName + " /mongodb/mongodb.key",
		"chown " + strconv.Itoa(int(defaultRunUID)) + " " + mongodContainerDataDir + " /mongodb/mongodb.key",
		"chmod 0400 /mongodb/mongodb.key",
	}
	return corev1.Container{
		Name:  "init",
		Image: "busybox",
		Command: []string{
			"/bin/sh", "-c", strings.Join(cmds, " && "),
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      mongodToolsVolName,
				MountPath: "/mongodb",
			},
			{
				Name:      mongodDataVolClaimName,
				MountPath: mongodContainerDataDir,
			},
			{
				Name:      m.Name + "-" + mongoDBKeySecretName,
				MountPath: mongoDBSecretsDir,
			},
		},
	}
}

func newPSMDBMongodContainer(m *v1alpha1.PerconaServerMongoDB) corev1.Container {
	mongoSpec := m.Spec.MongoDB
	//cpuQuantity := resource.NewQuantity(mongoSpec.Cpus, resource.DecimalSI)
	memoryQuantity := resource.NewQuantity(mongoSpec.Memory*1024*1024, resource.DecimalSI)

	args := []string{
		"--bind_ip_all",
		"--auth",
		"--keyFile=/mongodb/mongodb.key",
		"--port=" + strconv.Itoa(int(mongoSpec.Port)),
		"--replSet=" + mongoSpec.ReplsetName,
		"--storageEngine=" + mongoSpec.StorageEngine,
		"--slowms=" + strconv.Itoa(int(mongoSpec.OperationProfiling.SlowMs)),
		"--profile=1",
	}
	if mongoSpec.StorageEngine == "wiredTiger" {
		args = append(args, fmt.Sprintf(
			"--wiredTigerCacheSizeGB=%.2f",
			getWiredTigerCacheSizeGB(memoryQuantity, mongoSpec.WiredTiger.CacheSizeRatio)),
		)
	}

	falsePtr := false
	return corev1.Container{
		Name:  mongodContainerName,
		Image: m.Spec.Image,
		Args:  args,
		Ports: []corev1.ContainerPort{
			{
				Name:          mongodPortName,
				HostPort:      mongoSpec.HostPort,
				ContainerPort: mongoSpec.Port,
			},
		},
		Env: newPSMDBContainerEnv(m),
		EnvFrom: []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "percona-server-mongodb-users",
					},
					Optional: &falsePtr,
				},
			},
		},
		WorkingDir: mongodContainerDataDir,
		LivenessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				Exec: &corev1.ExecAction{
					Command: []string{
						"/mongodb/mongodb-healthcheck",
						"k8s",
						"liveness",
					},
				},
			},
			InitialDelaySeconds: int32(45),
			TimeoutSeconds:      int32(2),
			PeriodSeconds:       int32(5),
			FailureThreshold:    int32(5),
		},
		ReadinessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt(int(mongoSpec.Port)),
				},
			},
			InitialDelaySeconds: int32(15),
			TimeoutSeconds:      int32(3),
			PeriodSeconds:       int32(5),
			FailureThreshold:    int32(6),
		},
		Resources: corev1.ResourceRequirements{
			//Limits: corev1.ResourceList{
			//	corev1.ResourceCPU:    *cpuQuantity,
			//	corev1.ResourceMemory: *memoryQuantity,
			//},
			//Requests: corev1.ResourceList{
			//	corev1.ResourceCPU:    *cpuQuantity,
			//	corev1.ResourceMemory: *memoryQuantity,
			//},
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser: &m.Spec.RunUID,
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      mongodToolsVolName,
				MountPath: "/mongodb",
				ReadOnly:  true,
			},
			{
				Name:      mongodDataVolClaimName,
				MountPath: mongodContainerDataDir,
			},
		},
	}
}
