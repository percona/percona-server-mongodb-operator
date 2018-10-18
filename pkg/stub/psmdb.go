package stub

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	gigaByte                 int64   = 1024 * 1024 * 1024
	minWiredTigerCacheSizeGB float64 = 0.25
)

var (
	defaultSize                     int32   = 3
	defaultImage                    string  = "perconalab/percona-server-mongodb:latest"
	defaultRunUID                   int64   = 1001
	defaultReplsetName              string  = "rs"
	defaultStorageEngine            string  = "wiredTiger"
	defaultMongodPort               int32   = 27017
	defaultWiredTigerCacheSizeRatio float64 = 0.5
	defaultOperationProfilingSlowMs int     = 100
	configSvrReplsetName            string  = "csReplSet"
	mongodContainerDataDir          string  = "/data/db"
	mongodContainerName             string  = "mongod"
	mongodDataVolClaimName          string  = "mongod-data"
	mongodToolsVolName              string  = "mongodb-tools"
	mongodPortName                  string  = "mongodb"
	mongodbHealthcheckUrl           string  = "https://github.com/percona/mongodb-orchestration-tools/releases/download/0.4.1/mongodb-healthcheck"
)

// addPSMDBSpecDefaults sets default values for unset config params
func addPSMDBSpecDefaults(spec *v1alpha1.PerconaServerMongoDBSpec) {
	if spec.Size == 0 {
		spec.Size = defaultSize
	}
	if spec.Image == "" {
		spec.Image = defaultImage
	}
	if spec.MongoDB == nil {
		spec.MongoDB = &v1alpha1.PerconaServerMongoDBSpecMongoDB{}
	}
	if len(spec.MongoDB.Replsets) == 0 {
		spec.MongoDB.Replsets = []*v1alpha1.PerconaServerMongoDBReplset{
			{
				Name: defaultReplsetName,
				Size: defaultSize,
			},
		}
	}
	if spec.MongoDB.Port == 0 {
		spec.MongoDB.Port = defaultMongodPort
	}
	if spec.MongoDB.StorageEngine == "" {
		spec.MongoDB.StorageEngine = defaultStorageEngine
	}
	if spec.MongoDB.StorageEngine == "wiredTiger" {
		if spec.MongoDB.WiredTiger == nil {
			spec.MongoDB.WiredTiger = &v1alpha1.PerconaServerMongoDBSpecMongoDBWiredTiger{}
		}
		if spec.MongoDB.WiredTiger.CacheSizeRatio == 0 {
			spec.MongoDB.WiredTiger.CacheSizeRatio = defaultWiredTigerCacheSizeRatio
		}
	}
	if spec.MongoDB.OperationProfiling == nil {
		spec.MongoDB.OperationProfiling = &v1alpha1.PerconaServerMongoDBSpecMongoDBOperationProfiling{
			SlowMs: defaultOperationProfilingSlowMs,
		}
	}
	if spec.RunUID == 0 {
		spec.RunUID = defaultRunUID
	}
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

// newPSMDBStatefulSet returns a PSMDB stateful set
func newPSMDBStatefulSet(m *v1alpha1.PerconaServerMongoDB, replsetName string) *appsv1.StatefulSet {
	addPSMDBSpecDefaults(&m.Spec)
	storageQuantity := resource.NewQuantity(m.Spec.MongoDB.Storage, resource.DecimalSI)

	ls := labelsForPerconaServerMongoDB(m)
	set := &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      replsetName,
			Namespace: m.Namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: m.Name,
			Replicas:    &m.Spec.Size,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Affinity:      newPSMDBPodAffinity(ls),
					DNSPolicy:     corev1.DNSClusterFirstWithHostNet,
					RestartPolicy: corev1.RestartPolicyAlways,
					InitContainers: []corev1.Container{
						newPSMDBInitContainer(m),
					},
					Containers: []corev1.Container{
						newPSMDBMongodContainer(m, replsetName),
					},
					SecurityContext: &corev1.PodSecurityContext{
						//Sysctls: []corev1.Sysctl{
						//	{
						//		Name:  "vm.swappiness",
						//		Value: "1",
						//	},
						//},
					},
					Volumes: []corev1.Volume{
						{
							Name: mongodToolsVolName,
						},
						{
							Name: m.Name + "-" + mongoDBKeySecretName,
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: m.Name + "-" + mongoDBKeySecretName,
								},
							},
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: mongodDataVolClaimName,
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{
							corev1.ReadWriteOnce,
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: *storageQuantity,
							},
						},
						Selector: &metav1.LabelSelector{
							MatchLabels: ls,
						},
					},
				},
			},
		},
	}
	addOwnerRefToObject(set, asOwner(m))
	return set
}

func newPSMDBPodAffinity(ls map[string]string) *corev1.Affinity {
	return &corev1.Affinity{
		// prefer to run mongo instances on separate hostnames
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
				{
					Weight: 100,
					PodAffinityTerm: corev1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: ls,
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		},
	}
}

func newPSMDBContainerEnv(replsetName string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name:  "MONGODB_REPLSET",
			Value: replsetName,
		},
	}
}

func newPSMDBInitContainer(m *v1alpha1.PerconaServerMongoDB) corev1.Container {
	// download mongodb-healthcheck, copy internal auth key and setup ownership+permissions
	cmds := []string{
		"wget -P /mongodb " + mongodbHealthcheckUrl,
		"chmod +x /mongodb/mongodb-healthcheck",
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

func newPSMDBMongodContainer(m *v1alpha1.PerconaServerMongoDB, replsetName string) corev1.Container {
	mongoSpec := m.Spec.MongoDB
	//cpuQuantity := resource.NewQuantity(mongoSpec.Cpus, resource.DecimalSI)
	memoryQuantity := resource.NewQuantity(mongoSpec.Memory*1024*1024, resource.DecimalSI)

	args := []string{
		"--bind_ip_all",
		"--auth",
		"--keyFile=/mongodb/mongodb.key",
		"--port=" + strconv.Itoa(int(mongoSpec.Port)),
		"--replSet=" + replsetName,
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
	if mongoSpec.Sharding {
		if replsetName == configSvrReplsetName {
			args = append(args, "--configsvr")
		} else {
			args = append(args, "--shardsvr")
		}
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
		Env: newPSMDBContainerEnv(replsetName),
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
						"readiness",
						"--replset=",
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

// newPSMDBService returns a core/v1 API Service
func newPSMDBService(m *v1alpha1.PerconaServerMongoDB) *corev1.Service {
	ls := labelsForPerconaServerMongoDB(m)
	service := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       m.Spec.MongoDB.Port,
					TargetPort: intstr.FromInt(int(m.Spec.MongoDB.Port)),
				},
			},
			ClusterIP: "None",
			Selector:  ls,
		},
	}
	addOwnerRefToObject(service, asOwner(m))
	return service
}
