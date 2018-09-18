package stub

import (
	"context"

	"github.com/timvaillancourt/percona-server-mongodb-operator/pkg/apis/cache/v1alpha1"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func NewHandler() sdk.Handler {
	return &Handler{}
}

type Handler struct {
	// Fill me
}

func (h *Handler) Handle(ctx context.Context, event sdk.Event) error {
	switch o := event.Object.(type) {
	case *v1alpha1.PerconaServerMongoDB:
		err := sdk.Create(newPSMDBPod(o))
		if err != nil && !errors.IsAlreadyExists(err) {
			logrus.Errorf("failed to create psmdb pod : %v", err)
			return err
		}
	}
	return nil
}

// newPSMDBPod
func newPSMDBPod(cr *v1alpha1.PerconaServerMongoDB) *corev1.Pod {
	labels := map[string]string{
		"app": "percona-server-mongodb",
	}
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "percona-server-mongodb",
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
			Containers: []corev1.Container{
				{
					Name:    "percona-server-mongodb",
					Image:   "percona/percona-server-mongodb:3.6",
					Command: []string{"--port=27017"},
					Ports: []corev1.ContainerPort{{
						Name:          "mongodb",
						HostPort:      int32(27017),
						ContainerPort: int32(27017),
						Protocol:      corev1.ProtocolTCP,
					}},
					VolumeMounts: []corev1.VolumeMount{{
						Name:      "mongodb-data",
						MountPath: "/data/db",
					}},
					ReadinessProbe: &corev1.Probe{
						Handler: corev1.Handler{
							TCPSocket: &corev1.TCPSocketAction{
								Port: intstr.FromInt(27017),
							},
						},
						InitialDelaySeconds: int32(60),
						TimeoutSeconds:      int32(5),
						PeriodSeconds:       int32(3),
						FailureThreshold:    int32(5),
					},
				},
			},
		},
	}
}
