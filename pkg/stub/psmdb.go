package stub

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/config"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/mongod"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// addSpecDefaults sets default values for unset config params
func (h *Handler) addSpecDefaults(m *v1alpha1.PerconaServerMongoDB) {
	spec := &m.Spec
	if spec.Version == "" {
		spec.Version = config.DefaultVersion
	}
	if spec.ImagePullPolicy == "" {
		spec.ImagePullPolicy = config.DefaultImagePullPolicy
	}
	if spec.Secrets == nil {
		spec.Secrets = &v1alpha1.SecretsSpec{}
	}
	if spec.Secrets.Key == "" {
		spec.Secrets.Key = config.DefaultKeySecretName
	}
	if spec.Secrets.Users == "" {
		spec.Secrets.Users = config.DefaultUsersSecretName
	}
	if spec.Mongod == nil {
		spec.Mongod = &v1alpha1.MongodSpec{}
	}
	if spec.Mongod.Net == nil {
		spec.Mongod.Net = &v1alpha1.MongodSpecNet{}
	}
	if spec.Mongod.Net.Port == 0 {
		spec.Mongod.Net.Port = config.DefaultMongodPort
	}
	if spec.Mongod.Storage == nil {
		spec.Mongod.Storage = &v1alpha1.MongodSpecStorage{}
	}
	if spec.Mongod.Storage.Engine == "" {
		spec.Mongod.Storage.Engine = config.DefaultStorageEngine
	}

	switch spec.Mongod.Storage.Engine {
	case v1alpha1.StorageEngineInMemory:
		if spec.Mongod.Storage.InMemory == nil {
			spec.Mongod.Storage.InMemory = &v1alpha1.MongodSpecInMemory{}
		}
		if spec.Mongod.Storage.InMemory.EngineConfig == nil {
			spec.Mongod.Storage.InMemory.EngineConfig = &v1alpha1.MongodSpecInMemoryEngineConfig{}
		}
		if spec.Mongod.Storage.InMemory.EngineConfig.InMemorySizeRatio == 0 {
			spec.Mongod.Storage.InMemory.EngineConfig.InMemorySizeRatio = config.DefaultInMemorySizeRatio
		}
	case v1alpha1.StorageEngineWiredTiger:
		if spec.Mongod.Storage.WiredTiger == nil {
			spec.Mongod.Storage.WiredTiger = &v1alpha1.MongodSpecWiredTiger{}
		}
		if spec.Mongod.Storage.WiredTiger.CollectionConfig == nil {
			spec.Mongod.Storage.WiredTiger.CollectionConfig = &v1alpha1.MongodSpecWiredTigerCollectionConfig{}
		}
		if spec.Mongod.Storage.WiredTiger.EngineConfig == nil {
			spec.Mongod.Storage.WiredTiger.EngineConfig = &v1alpha1.MongodSpecWiredTigerEngineConfig{}
		}
		if spec.Mongod.Storage.WiredTiger.EngineConfig.CacheSizeRatio == 0 {
			spec.Mongod.Storage.WiredTiger.EngineConfig.CacheSizeRatio = config.DefaultWiredTigerCacheSizeRatio
		}
		if spec.Mongod.Storage.WiredTiger.IndexConfig == nil {
			spec.Mongod.Storage.WiredTiger.IndexConfig = &v1alpha1.MongodSpecWiredTigerIndexConfig{
				PrefixCompression: true,
			}
		}
	}

	if spec.Mongod.OperationProfiling == nil {
		spec.Mongod.OperationProfiling = &v1alpha1.MongodSpecOperationProfiling{
			Mode: config.DefaultOperationProfilingMode,
		}
	}
	if len(spec.Replsets) == 0 {
		spec.Replsets = []*v1alpha1.ReplsetSpec{{
			Name: config.DefaultReplsetName,
			Size: config.DefaultMongodSize,
		}}
	} else {
		for _, replset := range spec.Replsets {
			if replset.Size == 0 {
				replset.Size = config.DefaultMongodSize
			}
		}
	}
	if spec.RunUID == 0 && util.GetPlatform(m, h.serverVersion) != v1alpha1.PlatformOpenshift {
		spec.RunUID = config.DefaultRunUID
	}
}

// newStatefulSet returns a PSMDB stateful set
func (h *Handler) newStatefulSet(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, resources corev1.ResourceRequirements) (*appsv1.StatefulSet, error) {
	h.addSpecDefaults(m)

	ls := util.LabelsForPerconaServerMongoDBReplset(m, replset)
	runUID := util.GetContainerRunUID(m, h.serverVersion)
	set := util.NewStatefulSet(m, m.Name+"-"+replset.Name)
	set.Spec = appsv1.StatefulSetSpec{
		ServiceName: m.Name + "-" + replset.Name,
		Replicas:    &replset.Size,
		Selector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: ls,
			},
			Spec: corev1.PodSpec{
				Affinity:      mongod.NewPodAffinity(ls),
				RestartPolicy: corev1.RestartPolicyAlways,
				Containers: []corev1.Container{
					mongod.NewContainer(m, replset, resources, runUID),
				},
				SecurityContext: &corev1.PodSecurityContext{
					FSGroup: runUID,
				},
				Volumes: []corev1.Volume{
					{
						Name: m.Spec.Secrets.Key,
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								DefaultMode: &secretFileMode,
								SecretName:  m.Spec.Secrets.Key,
								Optional:    &util.FalseVar,
							},
						},
					},
				},
			},
		},
		VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
			util.NewPersistentVolumeClaim(m, resources, mongod.MongodDataVolClaimName, replset.StorageClass),
		},
	}
	util.AddOwnerRefToObject(set, util.AsOwner(m))
	return set, nil
}

// newService returns a core/v1 API Service
func newService(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) *corev1.Service {
	ls := util.LabelsForPerconaServerMongoDBReplset(m, replset)
	service := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-" + replset.Name,
			Namespace: m.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       mongod.MongodPortName,
					Port:       m.Spec.Mongod.Net.Port,
					TargetPort: intstr.FromInt(int(m.Spec.Mongod.Net.Port)),
				},
			},
			ClusterIP: "None",
			Selector:  ls,
		},
	}
	util.AddOwnerRefToObject(service, util.AsOwner(m))
	return service
}
