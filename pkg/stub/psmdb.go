package stub

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	defaultSize                     int32   = 3
	defaultImage                    string  = "perconalab/percona-server-mongodb:latest"
	defaultRunUID                   int64   = 1001
	defaultReplsetName              string  = "rs"
	defaultStorageEngine                    = v1alpha1.StorageEngineWiredTiger
	defaultMongodPort               int32   = 27017
	defaultWiredTigerCacheSizeRatio float64 = 0.5
	defaultOperationProfilingMode           = v1alpha1.OperationProfilingModeSlowOp
	defaultOperationProfilingSlowMs int     = 100
	mongodContainerDataDir          string  = "/data/db"
	mongodContainerName             string  = "mongod"
	mongodDataVolClaimName          string  = "mongod-data"
	mongodToolsVolName              string  = "mongodb-tools"
	mongodPortName                  string  = "mongodb"
	mongodbInitiatorUrl             string  = "https://github.com/percona/mongodb-orchestration-tools/releases/download/0.4.1/k8s-mongodb-initiator"
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
	if spec.Mongod == nil {
		spec.Mongod = &v1alpha1.MongodSpec{}
	}
	if spec.Mongod.ReplsetName == "" {
		spec.Mongod.ReplsetName = defaultReplsetName
	}
	if spec.Mongod.Port == 0 {
		spec.Mongod.Port = defaultMongodPort
	}
	if spec.Mongod.StorageEngine == "" {
		spec.Mongod.StorageEngine = defaultStorageEngine
	}
	if spec.Mongod.StorageEngine == v1alpha1.StorageEngineWiredTiger {
		if spec.Mongod.WiredTiger == nil {
			spec.Mongod.WiredTiger = &v1alpha1.MongodSpecWiredTiger{}
		}
		if spec.Mongod.WiredTiger.CacheSizeRatio == 0 {
			spec.Mongod.WiredTiger.CacheSizeRatio = defaultWiredTigerCacheSizeRatio
		}
	}
	if spec.Mongod.OperationProfiling == nil {
		spec.Mongod.OperationProfiling = &v1alpha1.MongodSpecOperationProfiling{
			Mode:              defaultOperationProfilingMode,
			SlowOpThresholdMs: defaultOperationProfilingSlowMs,
		}
	}
	if spec.RunUID == 0 {
		spec.RunUID = defaultRunUID
	}
}

// newPSMDBStatefulSet returns a PSMDB stateful set
func newPSMDBStatefulSet(m *v1alpha1.PerconaServerMongoDB) (*appsv1.StatefulSet, error) {
	addPSMDBSpecDefaults(&m.Spec)

	resources, err := parseSpecResources(m)
	if err != nil {
		return nil, err
	}

	ls := labelsForPerconaServerMongoDB(m)
	set := &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Spec.Mongod.ReplsetName,
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
						newPSMDBMongodContainer(m, resources),
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
								corev1.ResourceStorage: resources.Requests[corev1.ResourceStorage],
							},
						},
					},
				},
			},
		},
	}
	addOwnerRefToObject(set, asOwner(m))
	return set, nil
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
					Port:       m.Spec.Mongod.Port,
					TargetPort: intstr.FromInt(int(m.Spec.Mongod.Port)),
				},
			},
			ClusterIP: "None",
			Selector:  ls,
		},
	}
	addOwnerRefToObject(service, asOwner(m))
	return service
}
