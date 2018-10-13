package stub

import (
	"fmt"
	"math"
	"strconv"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
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
	defaultRunGID                   int64   = 1001
	defaultReplsetName              string  = "rs"
	defaultStorageEngine            string  = "wiredTiger"
	defaultMongodPort               int32   = 27017
	defaultWiredTigerCacheSizeRatio float64 = 0.5
	mongodContainerDataDir          string  = "/data/db"
	mongodContainerName             string  = "mongod"
	mongodPortName                  string  = "mongodb"
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
	if spec.MongoDB.ReplsetName == "" {
		spec.MongoDB.ReplsetName = defaultReplsetName
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
	if spec.RunGID == 0 {
		spec.RunGID = defaultRunGID
	}
	if spec.RunUID == 0 {
		spec.RunUID = defaultRunUID
	}
	//	if len(spec.Credentials) < 1 {
	//		spec.Credentials = []v1alpha1.PerconaServerMongoDBSpecCredential{
	//			{
	//				Username: "clusterAdmin",
	//				Password: "clusterAdminPassword",
	//				Role:     "clusterAdmin",
	//			},
	//			{
	//				Username: "clusterMonitor",
	//				Password: "clusterMonitorPassword",
	//				Role:     "clusterMonitor",
	//			},
	//			{
	//				Username: "userAdmin",
	//				Password: "userAdminPassword",
	//				Role:     "userAdmin",
	//			},
	//		}
	//	}
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
func newPSMDBStatefulSet(m *v1alpha1.PerconaServerMongoDB) *appsv1.StatefulSet {
	addPSMDBSpecDefaults(&m.Spec)
	ls := labelsForPerconaServerMongoDB(m.Name)
	set := &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
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
					Containers: []corev1.Container{
						newPSMDBMongodContainer(m),
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

func newPSMDBContainerEnv(m *v1alpha1.PerconaServerMongoDB) []corev1.EnvVar {
	mSpec := m.Spec.MongoDB
	return []corev1.EnvVar{
		{
			Name:  "MONGODB_PRIMARY_ADDR",
			Value: "127.0.0.1:" + strconv.Itoa(int(mSpec.Port)),
		},
		{
			Name:  "MONGODB_REPLSET",
			Value: mSpec.ReplsetName,
		},
		{
			Name:  "MONGODB_USER_ADMIN_USER",
			Value: "userAdmin",
		},
		{
			Name:  "MONGODB_USER_ADMIN_PASSWORD",
			Value: "admin123456",
		},
	}
}

func newPSMDBReplsetInitJob(m *v1alpha1.PerconaServerMongoDB) *batchv1.Job {
	ls := labelsForPerconaServerMongoDB(m.Name)
	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1",
			Kind:       "Job",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: batchv1.JobSpec{
			//			Selector: &metav1.LabelSelector{
			//				MatchLabels: ls,
			//			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					DNSPolicy:     corev1.DNSClusterFirstWithHostNet,
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:            "replset-init",
							Command:         []string{"dcos-mongodb-controller"},
							Args:            []string{"replset", "init"},
							Image:           "perconalab/mongodb-orchestration-tools:0.4.1-dcos",
							ImagePullPolicy: corev1.PullAlways,
							Env:             newPSMDBContainerEnv(m),
						},
					},
				},
			},
		},
	}
}

func newPSMDBMongodContainer(m *v1alpha1.PerconaServerMongoDB) corev1.Container {
	cpuQuantity := resource.NewQuantity(1, resource.DecimalSI)
	memoryQuantity := resource.NewQuantity(1024*1024*1024, resource.DecimalSI)

	mongoSpec := m.Spec.MongoDB
	args := []string{
		"--port=" + strconv.Itoa(int(mongoSpec.Port)),
		"--replSet=" + mongoSpec.ReplsetName,
		"--storageEngine=" + mongoSpec.StorageEngine,
	}
	if mongoSpec.StorageEngine == "wiredTiger" {
		args = append(args, fmt.Sprintf(
			"--wiredTigerCacheSizeGB=%.2f",
			getWiredTigerCacheSizeGB(memoryQuantity, mongoSpec.WiredTiger.CacheSizeRatio)),
		)
	}

	return corev1.Container{
		Name:            mongodContainerName,
		Image:           m.Spec.Image,
		ImagePullPolicy: corev1.PullAlways,
		Args:            args,
		Ports: []corev1.ContainerPort{
			{
				Name:          mongodPortName,
				HostPort:      mongoSpec.Port,
				ContainerPort: mongoSpec.Port,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		Env: newPSMDBContainerEnv(m),
		EnvFrom: []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "percona-server-mongodb",
					},
				},
			},
		},
		WorkingDir: mongodContainerDataDir,
		ReadinessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt(int(mongoSpec.Port)),
				},
			},
			InitialDelaySeconds: int32(60),
			TimeoutSeconds:      int32(5),
			PeriodSeconds:       int32(3),
			FailureThreshold:    int32(5),
		},
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    *cpuQuantity,
				corev1.ResourceMemory: *memoryQuantity,
			},
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:  &m.Spec.RunUID,
			RunAsGroup: &m.Spec.RunGID,
		},
	}
}
