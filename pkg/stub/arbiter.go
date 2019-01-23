package stub

import (
	"fmt"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/mongod"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"math"
)

func (h *Handler) ensureReplsetArbiter(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, resources corev1.ResourceRequirements) (*appsv1.StatefulSet, error) {
	logrus.Info("INSIDE ARBITER ensureReplsetArbiter")

	arbiterRightsizing(replset)

	arbiter := util.NewStatefulSet(m, m.Name+"-"+replset.Name+"-arbiter")

	if err := h.client.Get(arbiter); err != nil {

		lf := logrus.Fields{
			"version": m.Spec.Version,
			"size":    replset.Size,
			"cpu":     replset.Limits.Cpu,
			"memory":  replset.Limits.Memory,
			"storage": replset.Limits.Storage,
		}

		if replset.StorageClass != "" {
			lf["storageClass"] = replset.StorageClass
		}

		arbiter, err := h.newArbiter(m, replset, resources)
		if err != nil {
			return nil, err
		}

		if err := h.client.Create(arbiter); err != nil {
			if !k8serrors.IsAlreadyExists(err) {
				return nil, err
			}
		} else {
			logrus.WithFields(lf).Infof("created arbiter stateful set for replset: %s", replset.Name)
		}
	}

	return h.handleArbiterUpdate(m, arbiter, replset, resources)
}

func (h *Handler) handleArbiterUpdate(m *v1alpha1.PerconaServerMongoDB, arbiter *appsv1.StatefulSet, replset *v1alpha1.ReplsetSpec, resources corev1.ResourceRequirements) (*appsv1.StatefulSet, error) {
	logrus.Info("INSIDE ARBITER handleArbiterUpdate")

	if replset.Arbiter != nil && arbiter.Spec.Replicas != nil && *arbiter.Spec.Replicas != replset.Arbiter.Size {
		arbiterRightsizing(replset)
		logrus.Infof("setting arbiters count to %d for replset: %s", replset.Arbiter.Size, replset.Name)
		arbiter.Spec.Replicas = &replset.Arbiter.Size
	}
	runUID := util.GetContainerRunUID(m, h.serverVersion)

	arbiter.Spec.Template.Spec.Containers = h.newArbiterContainers(m, replset, resources, runUID)
	err := h.client.Update(arbiter)
	if err != nil {
		return nil, fmt.Errorf("failed to update arbiter stateful set for replset %s: %v", replset.Name, err)
	}
	return arbiter, nil
}

func (h *Handler) newArbiter(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, resources corev1.ResourceRequirements) (*appsv1.StatefulSet, error) {
	h.addSpecDefaults(m)

	runUID := util.GetContainerRunUID(m, h.serverVersion)

	ls := util.LabelsForPerconaServerMongoDBReplset(m, replset)
	ls["arbiter"] = "true"

	for k, v := range ls {
		logrus.Info("LABEL KEY:", k, "LABEL VALUE:", v)
	}

	arbiter := util.NewStatefulSet(m, m.Name+"-"+replset.Name+"-arbiter")

	arbiter.Spec = appsv1.StatefulSetSpec{
		ServiceName: m.Name + "-" + replset.Name + "-arbiter",
		Replicas:    &replset.Arbiter.Size,
		Selector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: ls,
			},
			Spec: corev1.PodSpec{
				//Affinity:      mongod.NewPodAffinity(ls),
				RestartPolicy: corev1.RestartPolicyAlways,
				Containers:    h.newArbiterContainers(m, replset, resources, runUID),
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
	util.AddOwnerRefToObject(arbiter, util.AsOwner(m))
	return arbiter, nil
}

func (h *Handler) newArbiterContainers(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, resources corev1.ResourceRequirements, runUID *int64) []corev1.Container {
	containers := []corev1.Container{
		mongod.NewArbiterContainer(m, replset, resources, runUID),
	}
	return containers
}

func arbiterRightsizing(replset *v1alpha1.ReplsetSpec) {
	if replset.Arbiter == nil {
		replset.Arbiter = &v1alpha1.Arbiter{
			Enabled: false,
			Size:    0,
		}
	}
	if replset.Arbiter.Enabled && replset.Arbiter.Size == 0 {
		replset.Arbiter.Size = 1
	}
	if replset.Arbiter.Enabled && replset.Arbiter.Size > 0 {
		if replset.Arbiter.Size >= replset.Size {
			replset.Arbiter.Size = int32(math.Floor(float64(replset.Size) * 0.43))
		}
	}
}
