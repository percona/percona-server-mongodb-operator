package stub

import (
	"fmt"
	"math"
	"strconv"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/cache/v1alpha1"

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
	defaultRunGID                   int64   = 1001
	defaultReplsetName              string  = "rs"
	defaultStorageEngine            string  = "wiredTiger"
	defaultMongodPort               int32   = 27017
	defaultWiredTigerCacheSizeRatio float64 = 0.5
	mongodContainerDataDir          string  = "/data/db"
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

// newPSMDBDeployment returns a PSMDB deployment
func newPSMDBDeployment(m *v1alpha1.PerconaServerMongoDB) *appsv1.Deployment {
	addPSMDBSpecDefaults(&m.Spec)

	ls := labelsForPerconaServerMongoDB(m.Name)
	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &m.Spec.Size,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{newPSMDBContainer(m)},
				},
			},
		},
	}
	addOwnerRefToObject(dep, asOwner(m))
	return dep
}

func newPSMDBContainer(m *v1alpha1.PerconaServerMongoDB) corev1.Container {
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
		Name:  m.Name,
		Image: m.Spec.Image,
		Args:  args,
		Ports: []corev1.ContainerPort{
			{
				Name:          mongodPortName,
				HostPort:      mongoSpec.Port,
				ContainerPort: mongoSpec.Port,
				Protocol:      corev1.ProtocolTCP,
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
				"cpu":    *cpuQuantity,
				"memory": *memoryQuantity,
			},
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:  &m.Spec.RunUID,
			RunAsGroup: &m.Spec.RunGID,
		},
	}
}
