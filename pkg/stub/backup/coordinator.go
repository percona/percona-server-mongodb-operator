package backup

import (
	"strconv"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	DefaultCoordinatorAPIPort = int32(10001)
	DefaultCoordinatorRPCPort = int32(10000)

	coordinatorContainerName = "backup-coordinator"
	coordinatorDataMount     = "/data"
	coordinatorDataVolume    = "backup-metadata"
	coordinatorAPIPortName   = "api"
	coordinatorRPCPortName   = "rpc"
)

var coordinatorLabels = map[string]string{
	"backup-coordinator": "true",
}

func (c *Controller) coordinatorAddress() string {
	return c.coordinatorStatefulSetName() + "." + c.psmdb.Namespace + ".svc.cluster.local"
}

func (c *Controller) coordinatorStatefulSetName() string {
	return c.psmdb.Name + "-backup-coordinator"
}

func (c *Controller) newCoordinatorPodSpec(resources corev1.ResourceRequirements) corev1.PodSpec {
	coordinatorSpec := c.psmdb.Spec.Backup.Coordinator
	return corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:            coordinatorContainerName,
				Image:           c.getImageName("coordinator"),
				ImagePullPolicy: c.psmdb.Spec.ImagePullPolicy,
				Args: []string{
					"--enable-clients-logging",
				},
				Env: []corev1.EnvVar{
					{
						Name:  "PBM_COORDINATOR_DEBUG",
						Value: strconv.FormatBool(c.psmdb.Spec.Backup.Coordinator.Debug),
					},
					{
						Name:  "PBM_COORDINATOR_API_PORT",
						Value: strconv.Itoa(int(coordinatorSpec.APIPort)),
					},
					{
						Name:  "PBM_COORDINATOR_GRPC_PORT",
						Value: strconv.Itoa(int(coordinatorSpec.RPCPort)),
					},
					{
						Name:  "PBM_COORDINATOR_WORK_DIR",
						Value: coordinatorDataMount,
					},
					{
						Name: "PBM_COORDINATOR_API_USERNAME",
						ValueFrom: util.EnvVarSourceFromSecret(
							c.psmdb.Spec.Secrets.Users,
							motPkg.EnvMongoDBBackupUser,
						),
					},
					{
						Name: "PBM_COORDINATOR_API_PASSWORD",
						ValueFrom: util.EnvVarSourceFromSecret(
							c.psmdb.Spec.Secrets.Users,
							motPkg.EnvMongoDBBackupPassword,
						),
					},
				},
				Resources: util.GetContainerResourceRequirements(resources),
				SecurityContext: &corev1.SecurityContext{
					RunAsNonRoot: &util.TrueVar,
					RunAsUser:    util.GetContainerRunUID(c.psmdb, c.serverVersion),
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      coordinatorDataVolume,
						MountPath: coordinatorDataMount,
					},
				},
				Ports: []corev1.ContainerPort{
					{
						Name:          coordinatorRPCPortName,
						ContainerPort: coordinatorSpec.RPCPort,
					},
					{
						Name:          coordinatorAPIPortName,
						ContainerPort: coordinatorSpec.APIPort,
					},
				},
				LivenessProbe: &corev1.Probe{
					InitialDelaySeconds: int32(5),
					TimeoutSeconds:      int32(3),
					Handler: corev1.Handler{
						TCPSocket: &corev1.TCPSocketAction{
							Port: intstr.FromInt(int(coordinatorSpec.RPCPort)),
						},
					},
				},
			},
		},
		SecurityContext: &corev1.PodSecurityContext{
			FSGroup: util.GetContainerRunUID(c.psmdb, c.serverVersion),
		},
	}
}

func (c *Controller) newCoordinatorStatefulSet() (*appsv1.StatefulSet, error) {
	resources, err := util.ParseResourceSpecRequirements(
		c.psmdb.Spec.Backup.Coordinator.Limits,
		c.psmdb.Spec.Backup.Coordinator.Requests,
	)
	if err != nil {
		return nil, err
	}

	ls := util.LabelsForPerconaServerMongoDB(c.psmdb, coordinatorLabels)
	set := &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.coordinatorStatefulSetName(),
			Namespace: c.psmdb.Namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: c.psmdb.Name,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: c.newCoordinatorPodSpec(resources),
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				util.NewPersistentVolumeClaim(c.psmdb, resources, coordinatorDataVolume, ""),
			},
		},
	}
	util.AddOwnerRefToObject(set, util.AsOwner(c.psmdb))
	return set, nil
}

func (c *Controller) newCoordinatorService() *corev1.Service {
	coordinatorSpec := c.psmdb.Spec.Backup.Coordinator
	service := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.psmdb.Name + "-backup-coordinator",
			Namespace: c.psmdb.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: util.LabelsForPerconaServerMongoDB(c.psmdb, coordinatorLabels),
			Ports: []corev1.ServicePort{
				{
					Name: coordinatorRPCPortName,
					Port: coordinatorSpec.RPCPort,
				},
				{
					Name: coordinatorAPIPortName,
					Port: coordinatorSpec.APIPort,
				},
			},
		},
	}
	util.AddOwnerRefToObject(service, util.AsOwner(c.psmdb))
	return service
}

func (c *Controller) DeleteCoordinator() error {
	set, err := c.newCoordinatorStatefulSet()
	if err != nil {
		return err
	}
	return c.client.Delete(set)
}

func (c *Controller) EnsureCoordinator() error {
	set, err := c.newCoordinatorStatefulSet()
	if err != nil {
		return err
	}

	err = c.client.Create(set)
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			err = c.client.Update(set)
			if err != nil {
				logrus.Infof("failed to update backup coordinator stateful set %s: %v", set.Name, err)
				return err
			}
		} else {
			logrus.Infof("failed to create backup coordinator stateful set %s: %v", set.Name, err)
			return err
		}
	} else {
		logrus.Infof("created backup coordinator stateful set: %s", set.Name)
	}

	service := c.newCoordinatorService()
	err = c.client.Create(service)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			logrus.Errorf("failed to create backup coordinator service %s: %v", service.Name, err)
			return err
		}
	} else {
		logrus.Infof("created backup coordinator service: %s", service.Name)
	}

	return nil
}
