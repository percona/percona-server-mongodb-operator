package stub

import (
	"context"
	"strconv"

	"github.com/timvaillancourt/percona-server-mongodb-operator/pkg/apis/cache/v1alpha1"

	"github.com/davecgh/go-spew/spew"
	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	mongodContainerPort int32 = 27017
)

type Config struct {
	PodName          string
	RunUser          int64
	RunGroup         int64
	ContainerDataDir string
	DataVolumeName   string
	PortName         string
	Image            string
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
		err := sdk.Create(h.newPSMDBPod(o))
		if err != nil && !errors.IsAlreadyExists(err) {
			logrus.Errorf("failed to create psmdb pod : %v", err)
			return err
		}
	}
	return nil
}

func (h *Handler) newPSMDBContainer(name string, port int32) corev1.Container {
	portStr := strconv.Itoa(int(port))
	return corev1.Container{
		Name:  name,
		Image: h.config.Image,
		Ports: []corev1.ContainerPort{
			{
				Name:          h.config.PortName,
				HostPort:      port,
				ContainerPort: mongodContainerPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      h.config.DataVolumeName,
				MountPath: h.config.ContainerDataDir,
			},
		},
		WorkingDir: h.config.ContainerDataDir,
		ReadinessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt(int(port)),
				},
			},
			InitialDelaySeconds: int32(60),
			TimeoutSeconds:      int32(5),
			PeriodSeconds:       int32(3),
			FailureThreshold:    int32(5),
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsUser:  &h.config.RunUser,
			RunAsGroup: &h.config.RunGroup,
		},
		Env: []corev1.EnvVar{
			{
				Name:  "MONGODB_PORT",
				Value: portStr,
			},
		},
	}
}

// newPSMDBPod
func (h *Handler) newPSMDBPod(cr *v1alpha1.PerconaServerMongoDB) *corev1.Pod {
	labels := map[string]string{
		"app": h.config.PodName,
	}

	containers := []corev1.Container{}
	mongods := map[string]int32{
		"percona-server-mongodb-0": 27017,
		"percona-server-mongodb-1": 27018,
		"percona-server-mongodb-2": 27019,
	}
	for name, port := range mongods {
		containers = append(containers, h.newPSMDBContainer(name, port))
	}

	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      h.config.PodName,
			Namespace: cr.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(cr, schema.GroupVersionKind{
					Group:   v1alpha1.SchemeGroupVersion.Group,
					Version: v1alpha1.SchemeGroupVersion.Version,
					Kind:    "PerconaServerMongoDB",
				}),
			},
			Labels: labels,
		},
		Spec: corev1.PodSpec{
			Containers: containers,
		},
	}
	spew.Dump(pod)

	return pod
}
