package backup

import (
	"encoding/json"
	"fmt"
	batchv1 "k8s.io/api/batch/v1"
	batchv1b "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/pkg/errors"
)

func BackupCronJob(backup *api.BackupTaskSpec, crName, namespace string, backupSpec api.BackupSpec, imagePullSecrets []corev1.LocalObjectReference) (batchv1b.CronJob, error) {
	jobName := crName + "-backup-" + backup.Name
	resources, err := psmdb.CreateResources(backupSpec.Resources)
	if err != nil {
		return batchv1b.CronJob{}, errors.Wrap(err, "cannot parse Backup resources")
	}

	containerArgs, err := newBackupCronJobContainerArgs(backup, jobName)
	if err != nil {
		return batchv1b.CronJob{}, errors.Wrap(err, "cannot generate container arguments")
	}

	backupPod := corev1.PodSpec{
		RestartPolicy:      corev1.RestartPolicyNever,
		ImagePullSecrets:   imagePullSecrets,
		ServiceAccountName: backupSpec.ServiceAccountName,
		Containers: []corev1.Container{
			{
				Name:    "backup",
				Image:   backupSpec.Image,
				Command: []string{"sh"},
				Env: []corev1.EnvVar{
					{
						Name:  "clusterName",
						Value: crName,
					},
					{
						Name: "NAMESPACE",
						ValueFrom: &corev1.EnvVarSource{
							FieldRef: &corev1.ObjectFieldSelector{
								FieldPath: "metadata.namespace",
							},
						},
					},
				},
				Args:            containerArgs,
				SecurityContext: backupSpec.ContainerSecurityContext,
				Resources:       resources,
			},
		},
		SecurityContext:  backupSpec.PodSecurityContext,
		RuntimeClassName: backupSpec.RuntimeClassName,
	}

	return batchv1b.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1beta1",
			Kind:       "CronJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        jobName,
			Namespace:   namespace,
			Labels:      NewBackupCronJobLabels(crName),
			Annotations: backupSpec.Annotations,
		},
		Spec: batchv1b.CronJobSpec{
			Schedule:          backup.Schedule,
			ConcurrencyPolicy: batchv1b.ForbidConcurrent,
			JobTemplate: batchv1b.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      NewBackupCronJobLabels(crName),
					Annotations: backupSpec.Annotations,
				},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: backupPod,
					},
				},
			},
		},
	}, nil
}

func NewBackupCronJobLabels(crName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "percona-server-mongodb",
		"app.kubernetes.io/instance":   crName,
		"app.kubernetes.io/replset":    "general",
		"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
		"app.kubernetes.io/component":  "backup-schedule",
		"app.kubernetes.io/part-of":    "percona-server-mongodb",
	}
}

func newBackupCronJobContainerArgs(backup *api.BackupTaskSpec, jobName string) ([]string, error) {
	backupCr := &api.PerconaServerMongoDBBackup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: api.SchemeGroupVersion.String(),
			Kind:       "PerconaServerMongoDBBackup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Finalizers:   []string{"delete-backup"},
			GenerateName: "cron-${clusterName:0:16}-$(date -u '+%Y%m%d%H%M%S')-",
			Labels: map[string]string{
				"ancestor": jobName,
				"cluster":  "${clusterName}",
				"type":     "cron",
			},
		},
		Spec: api.PerconaServerMongoDBBackupSpec{
			ClusterName:      "${clusterName}",
			StorageName:      backup.StorageName,
			Compression:      backup.CompressionType,
			CompressionLevel: backup.CompressionLevel,
		},
	}

	err := backupCr.CheckFields()
	if err != nil {
		return nil, err
	}

	backupCrJson, err := json.Marshal(backupCr)
	if err != nil {
		return nil, err
	}

	return []string{
		"-c",
		fmt.Sprintf(`curl \
			-vvv \
			-X POST \
			--cacert /run/secrets/kubernetes.io/serviceaccount/ca.crt \
			-H "Content-Type: application/json" \
			-H "Authorization: Bearer $(cat /run/secrets/kubernetes.io/serviceaccount/token)" \
			--data %q \
			https://${KUBERNETES_SERVICE_HOST}:${KUBERNETES_SERVICE_PORT}/apis/%s/namespaces/${NAMESPACE}/perconaservermongodbbackups`,
			backupCrJson, api.SchemeGroupVersion.String()),
	}, nil
}
