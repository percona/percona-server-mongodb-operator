package stub

import (
	"context"

	"github.com/timvaillancourt/percona-server-mongodb-operator/pkg/apis/cache/v1alpha1"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	mongodContainerDataDir       = "/data/db"
	mongodContainerPort    int32 = 27017
	mongodDataVolumeName         = "mongodb-data"
	mongodPortName               = "mongodb"
)

type Config struct {
	NodeCount   uint
	PodName     string
	ReplsetName string
	RunUser     int64
	RunGroup    int64
	Image       string
}

// labelsForPerconaServerMongoDB returns the labels for selecting the resources
// belonging to the given memcached CR name.
func labelsForPerconaServerMongoDB(name string) map[string]string {
	return map[string]string{
		"app":          "percona-server-mongodb",
		"memcached_cr": name,
	}
}

// addOwnerRefToObject appends the desired OwnerReference to the object
func addOwnerRefToObject(obj metav1.Object, ownerRef metav1.OwnerReference) {
	obj.SetOwnerReferences(append(obj.GetOwnerReferences(), ownerRef))
}

// asOwner returns an OwnerReference set as the memcached CR
func asOwner(m *v1alpha1.PerconaServerMongoDB) metav1.OwnerReference {
	trueVar := true
	return metav1.OwnerReference{
		APIVersion: m.APIVersion,
		Kind:       m.Kind,
		Name:       m.Name,
		UID:        m.UID,
		Controller: &trueVar,
	}
}

func NewHandler(config *Config) sdk.Handler {
	return &Handler{
		config: config,
	}
}

type Handler struct {
	config *Config
}

func (h *Handler) Handle(ctx context.Context, event sdk.Event) error {
	switch o := event.Object.(type) {
	case *v1alpha1.PerconaServerMongoDB:
		err := sdk.Create(h.newPSMDBDeployment(o))
		if err != nil && !errors.IsAlreadyExists(err) {
			logrus.Errorf("failed to create psmdb pod : %v", err)
			return err
		}
	}
	return nil
}

func (h *Handler) newPSMDBDeployment(m *v1alpha1.PerconaServerMongoDB) *appsv1.Deployment {
	ls := labelsForPerconaServerMongoDB(m.Name)
	replicas := m.Spec.Size
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
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						h.newPSMDBContainer(
							"percona-server-mongodb",
							int32(27017),
						),
					},
				},
			},
		},
	}
	addOwnerRefToObject(dep, asOwner(m))
	return dep
}

func (h *Handler) newPSMDBContainer(name string, port int32) corev1.Container {
	//portStr := strconv.Itoa(int(port))
	//cpuQuantity := resource.NewQuantity(1, resource.DecimalSI)
	//memoryQuantity := resource.NewQuantity(1024*1024*1024, resource.DecimalSI)
	//storageQuantity := resource.NewQuantity(8*1024*1024*1024, resource.DecimalSI)
	return corev1.Container{
		Name:  name,
		Image: h.config.Image,
		Ports: []corev1.ContainerPort{
			{
				Name:          mongodPortName,
				HostPort:      port,
				ContainerPort: mongodContainerPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		//VolumeMounts: []corev1.VolumeMount{
		//	{
		//		Name:      mongodDataVolumeName,
		//		MountPath: mongodContainerDataDir,
		//	},
		//},
		WorkingDir: mongodContainerDataDir,
		//ReadinessProbe: &corev1.Probe{
		//	Handler: corev1.Handler{
		//		TCPSocket: &corev1.TCPSocketAction{
		//			Port: intstr.FromInt(int(port)),
		//		},
		//	},
		//	InitialDelaySeconds: int32(60),
		//	TimeoutSeconds:      int32(5),
		//	PeriodSeconds:       int32(3),
		//	FailureThreshold:    int32(5),
		//},
		//Resources: corev1.ResourceRequirements{
		//	Limits: corev1.ResourceList{
		//		"cpu":    *cpuQuantity,
		//		"memory": *memoryQuantity,
		//		"storage": *storageQuantity,
		//	},
		//},
		//SecurityContext: &corev1.SecurityContext{
		//	RunAsUser:  &h.config.RunUser,
		//	RunAsGroup: &h.config.RunGroup,
		//},
		//Env: []corev1.EnvVar{
		//	{
		//		Name:  "MONGODB_PORT",
		//		Value: portStr,
		//	},
		//},
	}
}
