package backup

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/pkg/errors"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/percona/percona-backup-mongodb/pbm/defs"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func BackupCronJob(cr *api.PerconaServerMongoDB, task *api.BackupTaskSpec) (batchv1.CronJob, error) {
	backupSpec := cr.Spec.Backup
	containerArgs, err := newBackupCronJobContainerArgs(cr, task)
	if err != nil {
		return batchv1.CronJob{}, errors.Wrap(err, "cannot generate container arguments")
	}

	backupPod := corev1.PodSpec{
		RestartPolicy:      corev1.RestartPolicyNever,
		ImagePullSecrets:   cr.Spec.ImagePullSecrets,
		ServiceAccountName: backupSpec.ServiceAccountName,
		Containers: []corev1.Container{
			{
				Name:    "backup",
				Image:   backupSpec.Image,
				Command: []string{"sh"},
				Env: []corev1.EnvVar{
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
				Resources:       backupSpec.Resources,
			},
		},
		SecurityContext:  backupSpec.PodSecurityContext,
		RuntimeClassName: backupSpec.RuntimeClassName,
	}

	return batchv1.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1",
			Kind:       "CronJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        task.Name,
			Namespace:   cr.Namespace,
			Labels:      naming.NewBackupCronJobLabels(cr, backupSpec.Labels),
			Annotations: backupSpec.Annotations,
		},
		Spec: batchv1.CronJobSpec{
			Schedule:          task.Schedule,
			ConcurrencyPolicy: batchv1.ForbidConcurrent,
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      naming.NewBackupCronJobLabels(cr, backupSpec.Labels),
					Annotations: backupSpec.Annotations,
				},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels:      naming.NewBackupCronJobLabels(cr, backupSpec.Labels),
							Annotations: backupSpec.Annotations,
						},
						Spec: backupPod,
					},
				},
			},
		},
	}, nil
}

func BackupFromTask(cr *api.PerconaServerMongoDB, task *api.BackupTaskSpec) (*api.PerconaServerMongoDBBackup, error) {
	shortClusterName := cr.Name
	if len(shortClusterName) > 16 {
		shortClusterName = shortClusterName[:16]
	}
	backupType := defs.LogicalBackup
	if len(task.Type) > 0 {
		backupType = task.Type
	}
	finalizers := []string{naming.FinalizerDeleteBackup}
	if r := task.GetRetention(cr); !r.DeleteFromStorage {
		finalizers = []string{}
	}
	backupCr := &api.PerconaServerMongoDBBackup{
		TypeMeta: metav1.TypeMeta{
			APIVersion: api.SchemeGroupVersion.String(),
			Kind:       "PerconaServerMongoDBBackup",
		},
		ObjectMeta: metav1.ObjectMeta{
			Finalizers:   finalizers,
			GenerateName: "cron-" + shortClusterName + "-" + time.Now().Format("20060102150405") + "-",
			Labels:       naming.ScheduledBackupLabels(cr, task),
		},
		Spec: api.PerconaServerMongoDBBackupSpec{
			Type:             backupType,
			ClusterName:      cr.Name,
			StorageName:      task.StorageName,
			Compression:      task.CompressionType,
			CompressionLevel: task.CompressionLevel,
		},
	}
	if err := backupCr.CheckFields(); err != nil {
		return nil, err
	}
	return backupCr, nil
}

func newBackupCronJobContainerArgs(cr *api.PerconaServerMongoDB, task *api.BackupTaskSpec) ([]string, error) {
	backupCr, err := BackupFromTask(cr, task)
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
			-X POST \
			--cacert /run/secrets/kubernetes.io/serviceaccount/ca.crt \
			-H "Content-Type: application/json" \
			-H "Authorization: Bearer $(cat /run/secrets/kubernetes.io/serviceaccount/token)" \
			--data %q \
			https://${KUBERNETES_SERVICE_HOST}:${KUBERNETES_SERVICE_PORT}/apis/%s/namespaces/${NAMESPACE}/perconaservermongodbbackups`,
			backupCrJson, api.SchemeGroupVersion.String()),
	}, nil
}
