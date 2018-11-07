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
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	gigaByte                 int64   = 1024 * 1024 * 1024
	minWiredTigerCacheSizeGB float64 = 0.25
	dockerImageBase          string  = "percona/percona-server-mongodb"
)

// getMongodPort returns the mongod port number as a string
func getMongodPort(container *corev1.Container) string {
	for _, port := range container.Ports {
		if port.Name == mongodPortName {
			return strconv.Itoa(int(port.ContainerPort))
		}
	}
	return ""
}

// isContainerAndPodRunning returns a boolean reflecting if
// a container and pod are in a running state
func isContainerAndPodRunning(pod corev1.Pod, containerName string) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name == containerName && container.State.Running != nil {
			return true
		}
	}
	return false
}

// The WiredTiger internal cache, by default, will use the larger of either 50% of
// (RAM - 1 GB), or 256 MB. For example, on a system with a total of 4GB of RAM the
// WiredTiger cache will use 1.5GB of RAM (0.5 * (4 GB - 1 GB) = 1.5 GB).
//
// In normal situations WiredTiger does this default-sizing correctly but under Docker
// containers WiredTiger fails to detect the memory limit of the Docker container. We
// explicitly set the WiredTiger cache size to fix this.
//
// https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.engineConfig.cacheSizeGB
//
func getWiredTigerCacheSizeGB(resourceList corev1.ResourceList, cacheRatio float64) float64 {
	maxMemory := resourceList[corev1.ResourceMemory]
	size := math.Floor(cacheRatio * float64(maxMemory.Value()-gigaByte))
	sizeGB := size / float64(gigaByte)
	if sizeGB < minWiredTigerCacheSizeGB {
		sizeGB = minWiredTigerCacheSizeGB
	}
	return sizeGB
}

// newPSMDBContainerEnv returns environment variables for a container
func newPSMDBContainerEnv(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) []corev1.EnvVar {
	mSpec := m.Spec.Mongod
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
			Value: strconv.Itoa(int(mSpec.Net.Port)),
		},
		{
			Name:  motPkg.EnvMongoDBReplset,
			Value: replset.Name,
		},
	}
}

func newPSMDBInitContainer(m *v1alpha1.PerconaServerMongoDB) corev1.Container {
	// download mongodb-healthcheck, copy internal auth key and setup ownership+permissions
	cmds := []string{
		"wget -P /mongodb " + mongodbHealthcheckUrl,
		"wget -P /mongodb " + mongodbInitiatorUrl,
		"chmod +x /mongodb/mongodb-healthcheck /mongodb/k8s-mongodb-initiator",
		"cp " + mongoDBSecretsDir + "/" + mongoDbSecretMongoKeyVal + " /mongodb/mongodb.key",
		"chown " + strconv.Itoa(int(m.Spec.RunUID)) + " " + mongodContainerDataDir + " /mongodb/mongodb.key",
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
				Name:      m.Spec.Secrets.Key,
				MountPath: mongoDBSecretsDir,
			},
		},
	}
}

func newPSMDBMongodContainer(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, clusterRole *v1alpha1.ClusterRole, resources *corev1.ResourceRequirements) corev1.Container {
	mongod := m.Spec.Mongod

	args := []string{
		"--bind_ip_all",
		"--auth",
		"--keyFile=/mongodb/mongodb.key",
		"--port=" + strconv.Itoa(int(mongod.Net.Port)),
		"--replSet=" + replset.Name,
		"--storageEngine=" + string(mongod.Storage.Engine),
	}

	if clusterRole != nil {
		switch *clusterRole {
		case v1alpha1.ClusterRoleConfigSvr:
			args = append(args, "--configsvr")
		case v1alpha1.ClusterRoleShardSvr:
			args = append(args, "--shardsvr")
		}
	}

	switch mongod.OperationProfiling.Mode {
	case v1alpha1.OperationProfilingModeAll:
		args = append(args, "--profile=2")
	case v1alpha1.OperationProfilingModeSlowOp:
		args = append(args,
			"--slowms="+strconv.Itoa(int(mongod.OperationProfiling.SlowOpThresholdMs)),
			"--profile=1",
		)
	}

	switch mongod.Storage.Engine {
	case v1alpha1.StorageEngineWiredTiger:
		args = append(args, fmt.Sprintf(
			"--wiredTigerCacheSizeGB=%.2f",
			getWiredTigerCacheSizeGB(resources.Limits, mongod.Storage.WiredTiger.CacheSizeRatio),
		))
	case v1alpha1.StorageEngineInMemory:
		args = append(args, fmt.Sprintf(
			"--inMemorySizeGB=%.2f",
			getWiredTigerCacheSizeGB(resources.Limits, mongod.Storage.InMemory.SizeRatio),
		))
	}

	return corev1.Container{
		Name:  mongodContainerName,
		Image: dockerImageBase + ":" + m.Spec.Version,
		Args:  args,
		Ports: []corev1.ContainerPort{
			{
				Name:          mongodPortName,
				HostPort:      mongod.Net.HostPort,
				ContainerPort: mongod.Net.Port,
			},
		},
		Env: newPSMDBContainerEnv(m, replset),
		EnvFrom: []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: m.Spec.Secrets.Users,
					},
					Optional: &falseVar,
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
					Port: intstr.FromInt(int(mongod.Net.Port)),
				},
			},
			InitialDelaySeconds: int32(10),
			TimeoutSeconds:      int32(2),
			PeriodSeconds:       int32(3),
			FailureThreshold:    int32(8),
		},
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resources.Limits[corev1.ResourceCPU],
				corev1.ResourceMemory: resources.Limits[corev1.ResourceMemory],
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resources.Requests[corev1.ResourceCPU],
				corev1.ResourceMemory: resources.Requests[corev1.ResourceMemory],
			},
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot: &trueVar,
			RunAsUser:    &m.Spec.RunUID,
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
