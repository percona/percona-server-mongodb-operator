package stub

import (
	"context"
	"strconv"

	"github.com/timvaillancourt/percona-server-mongodb-operator/pkg/apis/cache/v1alpha1"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	mongodContainerDataDir = "/data/db"
	mongodPortName         = "mongodb"
)

func NewHandler() sdk.Handler {
	return &Handler{}
}

type Handler struct{}

func (h *Handler) Handle(ctx context.Context, event sdk.Event) error {
	switch o := event.Object.(type) {
	case *v1alpha1.PerconaServerMongoDB:
		err := sdk.Create(newPSMDBDeployment(o))
		if err != nil && !errors.IsAlreadyExists(err) {
			logrus.Errorf("failed to create psmdb pod : %v", err)
			return err
		}
	}
	return nil
}

// labelsForPerconaServerMongoDB returns the labels for selecting the resources
// belonging to the given memcached CR name.
func labelsForPerconaServerMongoDB(name string) map[string]string {
	return map[string]string{
		"app":                       "percona-server-mongodb",
		"percona-server-mongodb_cr": name,
	}
}

// addOwnerRefToObject appends the desired OwnerReference to the object
func addOwnerRefToObject(obj metav1.Object, ownerRef metav1.OwnerReference) {
	obj.SetOwnerReferences(append(obj.GetOwnerReferences(), ownerRef))
}

// asOwner returns an OwnerReference set as the PerconaServerMongoDB CR
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

func addPSMDBSpecDefaults(spec v1alpha1.PerconaServerMongoDBSpec) v1alpha1.PerconaServerMongoDBSpec {
	if spec.Image == "" {
		spec.Image = "percona/percona-server-mongodb:latest"
	}
	if spec.MongoDB == nil {
		spec.MongoDB = &v1alpha1.PerconaServerMongoDBSpecMongoDB{}
	}
	if spec.MongoDB.Port == 0 {
		spec.MongoDB.Port = int32(27017)
	}
	if spec.MongoDB.StorageEngine == "" {
		spec.MongoDB.StorageEngine = "wiredTiger"
	}
	if spec.RunGID == 0 {
		spec.RunGID = int64(1001)
	}
	if spec.RunUID == 0 {
		spec.RunUID = int64(1001)
	}
	return spec
}

// newPSMDBDeployment returns a PSMDB deployment
func newPSMDBDeployment(m *v1alpha1.PerconaServerMongoDB) *appsv1.Deployment {
	m.Spec = addPSMDBSpecDefaults(m.Spec)
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
	return corev1.Container{
		Name:  "percona-server-mongodb",
		Image: m.Spec.Image,
		Args: []string{
			"--port" + strconv.Itoa(int(m.Spec.MongoDB.Port)),
			"--storageEngine" + m.Spec.MongoDB.StorageEngine,
		},
		Ports: []corev1.ContainerPort{
			{
				Name:          mongodPortName,
				HostPort:      m.Spec.MongoDB.Port,
				ContainerPort: m.Spec.MongoDB.Port,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		WorkingDir: mongodContainerDataDir,
		ReadinessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt(int(m.Spec.MongoDB.Port)),
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
