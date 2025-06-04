package perconaservermongodbrestore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm/defs"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

var anotherOpBackoff = wait.Backoff{
	Steps:    13,
	Duration: time.Second,
	Factor:   2.0,
	Jitter:   0.1,
	Cap:      15 * time.Minute,
}

// reconcilePhysicalRestore performs a physical restore of a Percona Server for MongoDB from a backup.
func (r *ReconcilePerconaServerMongoDBRestore) reconcilePhysicalRestore(
	ctx context.Context,
	cr *psmdbv1.PerconaServerMongoDBRestore,
	bcp *psmdbv1.PerconaServerMongoDBBackup,
	cluster *psmdbv1.PerconaServerMongoDB,
) (psmdbv1.PerconaServerMongoDBRestoreStatus, error) {
	log := logf.FromContext(ctx)
	var err error

	status := cr.Status

	replsets := cluster.Spec.Replsets
	if cluster.Spec.Sharding.Enabled {
		replsets = append(replsets, cluster.Spec.Sharding.ConfigsvrReplSet)
	}

	if cr.Status.State == psmdbv1.RestoreStateNew {
		pod := corev1.Pod{}
		podName := replsets[0].PodName(cluster, 0)
		if err := r.client.Get(ctx, types.NamespacedName{Name: podName, Namespace: cluster.Namespace}, &pod); err != nil {
			return status, errors.Wrapf(err, "get pod/%s", podName)
		}

		if err := retry.OnError(anotherOpBackoff, func(err error) bool {
			return strings.Contains(err.Error(), "another operation")
		}, func() error {
			return r.disablePITR(ctx, &pod)
		}); err != nil {
			return status, errors.Wrap(err, "disable pitr")
		}

		if cr.Spec.PITR != nil {
			var ts string
			switch cr.Spec.PITR.Type {
			case psmdbv1.PITRestoreTypeDate:
				ts = cr.Spec.PITR.Date.Format("2006-01-02T15:04:05")
			case psmdbv1.PITRestoreTypeLatest:
				ts, err = r.getLatestChunkTS(ctx, &pod)
				if err != nil {
					return status, errors.Wrap(err, "get latest chunk timestamp")
				}
			}

			status.PITRTarget = ts
		}

		if err := r.updatePBMConfigSecret(ctx, cr, cluster, bcp); err != nil {
			return status, errors.Wrap(err, "update PBM config secret")
		}

		status.State = psmdbv1.RestoreStateWaiting
	}

	if err := r.prepareStatefulSetsForPhysicalRestore(ctx, cluster); err != nil {
		return status, errors.Wrap(err, "prepare statefulsets for physical restore")
	}

	sfsReady, err := r.checkIfStatefulSetsAreReadyForPhysicalRestore(ctx, cluster)
	if err != nil {
		return status, errors.Wrap(err, "check if statefulsets are ready for physical restore")
	}

	if (!sfsReady && cr.Status.State != psmdbv1.RestoreStateRunning) || cr.Status.State == psmdbv1.RestoreStateNew {
		log.Info("Waiting for statefulsets to be ready before restore", "ready", sfsReady)
		return status, nil
	}

	if cr.Status.State == psmdbv1.RestoreStateWaiting && sfsReady && cr.Spec.PITR != nil {
		rsReady, err := r.checkIfReplsetsAreReadyForPhysicalRestore(ctx, cluster)
		if err != nil {
			return status, errors.Wrap(err, "check if replsets are ready for physical restore")
		}

		if !rsReady {
			if err := r.prepareReplsetsForPhysicalRestore(ctx, cluster); err != nil {
				return status, errors.Wrap(err, "prepare replsets for physical restore")
			}

			log.Info("Waiting for replsets to be ready before restore", "ready", rsReady)
			return status, nil
		}
	}

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	if cr.Status.State == psmdbv1.RestoreStateWaiting {
		rs := replsets[0]

		pbmAgentsReady, err := r.checkIfPBMAgentsReadyForPhysicalRestore(ctx, cluster)
		if err != nil {
			return status, errors.Wrap(err, "check if pbm agents are ready")
		}

		if !pbmAgentsReady {
			log.Info("Waiting for pbm-agents to be ready before restore", "ready", pbmAgentsReady)
			return status, nil
		}

		pod := corev1.Pod{}
		if err := r.client.Get(ctx, types.NamespacedName{Name: rs.PodName(cluster, 0), Namespace: cluster.Namespace}, &pod); err != nil {
			return status, errors.Wrap(err, "get pod")
		}

		var restoreCommand []string
		if cr.Spec.PITR != nil {
			restoreCommand = []string{"/opt/percona/pbm", "restore", "--base-snapshot", bcp.Status.PBMname, "--time", cr.Status.PITRTarget, "--out", "json"}
		} else {
			restoreCommand = []string{"/opt/percona/pbm", "restore", bcp.Status.PBMname, "--out", "json"}
		}

		err = retry.OnError(anotherOpBackoff, func(err error) bool {
			return strings.Contains(err.Error(), "another operation") ||
				strings.Contains(err.Error(), "unable to upgrade connection")
		}, func() error {
			log.Info("Starting restore", "command", restoreCommand, "pod", pod.Name)

			stdoutBuf.Reset()
			stderrBuf.Reset()

			err := r.clientcmd.Exec(ctx, &pod, "mongod", restoreCommand, nil, stdoutBuf, stderrBuf, false)
			if err != nil {
				log.Error(nil, "Restore failed to start", "pod", pod.Name, "stderr", stderrBuf.String(), "stdout", stdoutBuf.String())
				return errors.Wrapf(err, "start restore stderr: %s stdout: %s", stderrBuf.String(), stdoutBuf.String())
			}

			log.Info("Restore started", "pod", pod.Name)

			return nil
		})
		if err != nil {
			return status, err
		}

		var out struct {
			Name    string `json:"name"`
			Storage string `json:"storage"`
		}
		if err := json.Unmarshal(stdoutBuf.Bytes(), &out); err != nil {
			return status, errors.Wrap(err, "unmarshal PBM restore output")
		}

		status.State = psmdbv1.RestoreStateRequested
		status.PBMname = out.Name

		return status, nil
	}

	meta := backup.BackupMeta{}

	err = retry.OnError(retry.DefaultBackoff, func(err error) bool {
		return strings.Contains(err.Error(), "container is not created or running") ||
			strings.Contains(err.Error(), "error dialing backend: No agent available") ||
			strings.Contains(err.Error(), "unable to upgrade connection") ||
			strings.Contains(err.Error(), "unmarshal PBM describe-restore output")
	}, func() error {
		stdoutBuf.Reset()
		stderrBuf.Reset()

		command := []string{
			"/opt/percona/pbm", "describe-restore", cr.Status.PBMname,
			"--config", "/etc/pbm/pbm_config.yaml",
			"--out", "json",
		}

		pod := corev1.Pod{}
		if err := r.client.Get(ctx, types.NamespacedName{Name: replsets[0].PodName(cluster, 0), Namespace: cluster.Namespace}, &pod); err != nil {
			return errors.Wrap(err, "get pod")
		}

		log.V(1).Info("Check restore status", "command", command, "pod", pod.Name)

		if err := r.clientcmd.Exec(ctx, &pod, "mongod", command, nil, stdoutBuf, stderrBuf, false); err != nil {
			return errors.Wrapf(err, "describe restore stderr: %s stdout: %s", stderrBuf.String(), stdoutBuf.String())
		}

		return nil
	})
	if err != nil {
		return status, err
	}

	if err := json.Unmarshal(stdoutBuf.Bytes(), &meta); err != nil {
		return status, errors.Wrap(err, "unmarshal PBM describe-restore output")
	}

	log.V(1).Info("PBM restore status", "status", meta)

	switch meta.Status {
	case defs.StatusStarting:
		for _, rs := range meta.Replsets {
			if rs.Status == defs.StatusRunning {
				status.State = psmdbv1.RestoreStateRunning
				return status, nil
			}
		}
	case defs.StatusError:
		status.State = psmdbv1.RestoreStateError
		status.Error = meta.Err
	case defs.StatusRunning:
		status.State = psmdbv1.RestoreStateRunning
	case defs.StatusDone:
		for _, rs := range meta.Replsets {
			if rs.Status == defs.StatusDone {
				continue
			}

			log.Info("Waiting replset restore to finish", "replset", rs.Name, "status", rs.Status)

			status.State = psmdbv1.RestoreStateRunning
			return status, nil
		}

		status.State = psmdbv1.RestoreStateReady
	}

	if status.State == psmdbv1.RestoreStateReady {
		replsets := cluster.Spec.Replsets
		if cluster.Spec.Sharding.Enabled {
			replsets = append(replsets, cluster.Spec.Sharding.ConfigsvrReplSet)
		}

		for _, rs := range replsets {
			stsName := naming.MongodStatefulSetName(cluster, rs)

			log.Info("Deleting statefulset", "statefulset", stsName)

			sts := appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      stsName,
					Namespace: cluster.Namespace,
				},
			}

			if err := r.client.Delete(ctx, &sts); err != nil {
				return status, errors.Wrapf(err, "delete statefulset %s", stsName)
			}

			if rs.NonVoting.Enabled {
				stsName := naming.NonVotingStatefulSetName(cluster, rs)

				log.Info("Deleting statefulset", "statefulset", stsName)

				sts := appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      stsName,
						Namespace: cluster.Namespace,
					},
				}

				if err := r.client.Delete(ctx, &sts); err != nil {
					return status, errors.Wrapf(err, "delete statefulset %s", stsName)
				}
			}

			if rs.Hidden.Enabled {
				stsName := naming.HiddenStatefulSetName(cluster, rs)

				log.Info("Deleting statefulset", "statefulset", stsName)

				sts := appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      stsName,
						Namespace: cluster.Namespace,
					},
				}

				if err := r.client.Delete(ctx, &sts); err != nil {
					return status, errors.Wrapf(err, "delete statefulset %s", stsName)
				}
			}

			if rs.Arbiter.Enabled {
				stsName := naming.ArbiterStatefulSetName(cluster, rs)

				log.Info("Deleting statefulset", "statefulset", stsName)

				sts := appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      stsName,
						Namespace: cluster.Namespace,
					},
				}

				if err := r.client.Delete(ctx, &sts); err != nil {
					return status, errors.Wrapf(err, "delete statefulset %s", stsName)
				}
			}
		}

		err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			c := &psmdbv1.PerconaServerMongoDB{}
			err := r.client.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}, c)
			if err != nil {
				return err
			}

			orig := c.DeepCopy()

			if c.Annotations == nil {
				c.Annotations = make(map[string]string)
			}
			c.Annotations[psmdbv1.AnnotationResyncPBM] = "true"

			return r.client.Patch(ctx, c, client.MergeFrom(orig))
		})
		if err != nil {
			return status, errors.Wrapf(err, "annotate psmdb/%s for PBM resync", cluster.Name)
		}

	}

	return status, nil
}

// updateStatefulSetForPhysicalRestore updates the StatefulSet to prepare it for a physical restore of PerconaServerMongoDB.
// This involves:
// - Annotating the StatefulSet to prevent psmdb_controller reconciliation.
// - Adding an init container that installs necessary tools for backup and restore.
// - Removing the existing backup-agent container.
// - Appending a volume for backup configuration.
// - Adjusting the primary container's command, environment variables, and volume mounts for the restore process.
// It returns an error if there's any issue during the update or if the backup-agent container is not found.
func (r *ReconcilePerconaServerMongoDBRestore) updateStatefulSetForPhysicalRestore(
	ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB, namespacedName types.NamespacedName, port int32) error {
	log := logf.FromContext(ctx)

	sts := appsv1.StatefulSet{}
	err := r.client.Get(ctx, namespacedName, &sts)
	if err != nil {
		return err
	}

	// Annotating statefulset to stop reconciliation in psmdb_controller
	if sts.Annotations == nil {
		sts.Annotations = make(map[string]string)
	}
	sts.Annotations[psmdbv1.AnnotationRestoreInProgress] = "true"

	cmd := []string{
		"bash", "-c",
		strings.Join([]string{
			"install -D /usr/bin/pbm /opt/percona/pbm",
			"install -D /usr/bin/pbm-agent /opt/percona/pbm-agent",
			"install -D /usr/bin/pbm-agent-entrypoint /opt/percona/pbm-agent-entrypoint",
		}, " && "),
	}
	pbmInit := psmdb.EntrypointInitContainer(
		cluster,
		"pbm-init",
		cluster.Spec.Backup.Image,
		cluster.Spec.ImagePullPolicy,
		cmd,
	)
	sts.Spec.Template.Spec.InitContainers = append(sts.Spec.Template.Spec.InitContainers, pbmInit)

	pbmIdx := -1
	for idx, c := range sts.Spec.Template.Spec.Containers {
		if c.Name == naming.ContainerBackupAgent {
			pbmIdx = idx
			break
		}
	}
	// remove backup-agent container
	if pbmIdx != -1 {
		sts.Spec.Template.Spec.Containers = append(sts.Spec.Template.Spec.Containers[:pbmIdx], sts.Spec.Template.Spec.Containers[pbmIdx+1:]...)
	}

	sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: "pbm-config",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: r.pbmConfigName(cluster),
			},
		},
	})
	sts.Spec.Template.Spec.Containers[0].VolumeMounts = append(sts.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      "pbm-config",
		MountPath: "/etc/pbm/",
		ReadOnly:  true,
	})
	sts.Spec.Template.Spec.Containers[0].VolumeMounts = append(sts.Spec.Template.Spec.Containers[0].VolumeMounts, cluster.Spec.Backup.VolumeMounts...)
	sts.Spec.Template.Spec.Containers[0].Command = []string{"/opt/percona/physical-restore-ps-entry.sh"}

	f := false
	pbmEnvVars := []corev1.EnvVar{
		{
			Name: "PBM_AGENT_MONGODB_USERNAME",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: "MONGODB_BACKUP_USER_ESCAPED",
					LocalObjectReference: corev1.LocalObjectReference{
						Name: api.UserSecretName(cluster),
					},
					Optional: &f,
				},
			},
		},
		{
			Name: "PBM_AGENT_MONGODB_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: "MONGODB_BACKUP_PASSWORD_ESCAPED",
					LocalObjectReference: corev1.LocalObjectReference{
						Name: api.UserSecretName(cluster),
					},
					Optional: &f,
				},
			},
		},
		{
			Name:  "PBM_AGENT_SIDECAR",
			Value: "true",
		},
		{
			Name:  "PBM_AGENT_SIDECAR_SLEEP",
			Value: "5",
		},
	}
	if cluster.CompareVersion("1.19.0") < 0 {
		for i, v := range pbmEnvVars {
			pbmEnvVars[i].ValueFrom.SecretKeyRef.Key = strings.TrimSuffix(v.ValueFrom.SecretKeyRef.Key, "_ESCAPED")
			pbmEnvVars[i].ValueFrom.SecretKeyRef.LocalObjectReference.Name = cluster.Spec.Secrets.Users
			pbmEnvVars[i].ValueFrom.SecretKeyRef.Optional = nil
		}
	}
	sts.Spec.Template.Spec.Containers[0].Env = append(sts.Spec.Template.Spec.Containers[0].Env, pbmEnvVars...)

	sslSecret := new(corev1.Secret)
	err = r.client.Get(ctx, types.NamespacedName{Name: api.SSLSecretName(cluster), Namespace: cluster.Namespace}, sslSecret)
	if client.IgnoreNotFound(err) != nil {
		return errors.Wrap(err, "check ssl secrets")
	}

	mongoDBURI := "mongodb://$(PBM_AGENT_MONGODB_USERNAME):$(PBM_AGENT_MONGODB_PASSWORD)@$(POD_NAME)"
	if cluster.CompareVersion("1.21.0") >= 0 {
		mongoDBURI = psmdb.BuildMongoDBURI(ctx, cluster.TLSEnabled(), sslSecret)

		sts.Spec.Template.Spec.Containers[0].Env = append(sts.Spec.Template.Spec.Containers[0].Env, []corev1.EnvVar{
			{
				Name:  "PBM_AGENT_TLS_ENABLED",
				Value: strconv.FormatBool(cluster.TLSEnabled()),
			},
			{
				Name:  "PBM_MONGODB_PORT",
				Value: strconv.Itoa(int(port)),
			},
		}...)
	}

	sts.Spec.Template.Spec.Containers[0].Env = append(sts.Spec.Template.Spec.Containers[0].Env, []corev1.EnvVar{
		{
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		{
			// This environment variable must be appended last because it may reference
			// other variables using the $(VAR_NAME) syntax, which only resolves correctly
			// if those variables are already defined above.
			Name:  "PBM_MONGODB_URI",
			Value: mongoDBURI,
		},
	}...)

	err = r.client.Update(ctx, &sts)
	if err != nil {
		return err
	}

	log.Info("Updated statefulset", "name", namespacedName.Name)
	return nil
}

func (r *ReconcilePerconaServerMongoDBRestore) prepareStatefulSetsForPhysicalRestore(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	replsets := cluster.Spec.Replsets
	if cluster.Spec.Sharding.Enabled {
		replsets = append(replsets, cluster.Spec.Sharding.ConfigsvrReplSet)
	}

	for _, rs := range replsets {
		stsName := naming.MongodStatefulSetName(cluster, rs)

		sts := appsv1.StatefulSet{}
		nn := types.NamespacedName{Namespace: cluster.Namespace, Name: stsName}
		err := r.client.Get(ctx, nn, &sts)
		if err != nil {
			return err
		}

		_, ok := sts.Annotations[psmdbv1.AnnotationRestoreInProgress]
		if ok {
			continue
		}

		log.Info("Preparing statefulset for physical restore", "name", stsName)

		err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			return r.updateStatefulSetForPhysicalRestore(ctx, cluster, types.NamespacedName{Namespace: cluster.Namespace, Name: stsName}, rs.GetPort())
		})
		if err != nil {
			return errors.Wrapf(err, "prepare statefulset %s for physical restore", stsName)
		}

		if rs.NonVoting.Enabled {
			stsName := naming.NonVotingStatefulSetName(cluster, rs)
			nn := types.NamespacedName{Namespace: cluster.Namespace, Name: stsName}

			log.Info("Preparing statefulset for physical restore", "name", stsName)

			err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				return r.updateStatefulSetForPhysicalRestore(ctx, cluster, nn, rs.GetPort())
			})
			if err != nil {
				return errors.Wrapf(err, "prepare statefulset %s for physical restore", stsName)
			}
		}

		if rs.Hidden.Enabled {
			stsName := naming.HiddenStatefulSetName(cluster, rs)
			nn := types.NamespacedName{Namespace: cluster.Namespace, Name: stsName}

			log.Info("Preparing statefulset for physical restore", "name", stsName)

			err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				return r.updateStatefulSetForPhysicalRestore(ctx, cluster, nn, rs.GetPort())
			})
			if err != nil {
				return errors.Wrapf(err, "prepare statefulset %s for physical restore", stsName)
			}
		}

		if rs.Arbiter.Enabled {
			stsName := naming.ArbiterStatefulSetName(cluster, rs)
			nn := types.NamespacedName{Namespace: cluster.Namespace, Name: stsName}

			log.Info("Preparing statefulset for physical restore", "name", stsName)

			err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				sts := appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      stsName,
						Namespace: cluster.Namespace,
					},
				}

				err := r.client.Get(ctx, nn, &sts)
				if err != nil {
					return err
				}

				orig := sts.DeepCopy()
				zero := int32(0)

				sts.Spec.Replicas = &zero

				if sts.Annotations == nil {
					sts.Annotations = make(map[string]string)
				}
				sts.Annotations[psmdbv1.AnnotationRestoreInProgress] = "true"

				return r.client.Patch(ctx, &sts, client.MergeFrom(orig))
			})
			if err != nil {
				return errors.Wrapf(err, "prepare statefulset %s for physical restore", stsName)
			}
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDBRestore) getUserCredentials(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB, role psmdbv1.SystemUserRole) (psmdb.Credentials, error) {
	creds := psmdb.Credentials{}

	usersSecret := corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{Name: psmdbv1.UserSecretName(cluster), Namespace: cluster.Namespace}, &usersSecret)
	if err != nil {
		return creds, errors.Wrap(err, "get secret")
	}

	switch role {
	case psmdbv1.RoleDatabaseAdmin:
		creds.Username = string(usersSecret.Data[psmdbv1.EnvMongoDBDatabaseAdminUser])
		creds.Password = string(usersSecret.Data[psmdbv1.EnvMongoDBDatabaseAdminPassword])
	case psmdbv1.RoleClusterAdmin:
		creds.Username = string(usersSecret.Data[psmdbv1.EnvMongoDBClusterAdminUser])
		creds.Password = string(usersSecret.Data[psmdbv1.EnvMongoDBClusterAdminPassword])
	case psmdbv1.RoleUserAdmin:
		creds.Username = string(usersSecret.Data[psmdbv1.EnvMongoDBUserAdminUser])
		creds.Password = string(usersSecret.Data[psmdbv1.EnvMongoDBUserAdminPassword])
	case psmdbv1.RoleClusterMonitor:
		creds.Username = string(usersSecret.Data[psmdbv1.EnvMongoDBClusterMonitorUser])
		creds.Password = string(usersSecret.Data[psmdbv1.EnvMongoDBClusterMonitorPassword])
	case psmdbv1.RoleBackup:
		creds.Username = string(usersSecret.Data[psmdbv1.EnvMongoDBBackupUser])
		creds.Password = string(usersSecret.Data[psmdbv1.EnvMongoDBBackupPassword])
	default:
		return creds, errors.Errorf("not implemented for role: %s", role)
	}

	return creds, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) runMongosh(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB, pod *corev1.Pod, eval string) (*bytes.Buffer, *bytes.Buffer, error) {
	log := logf.FromContext(ctx)

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	creds, err := r.getUserCredentials(ctx, cluster, psmdbv1.RoleClusterAdmin)
	if err != nil {
		return stdoutBuf, stderrBuf, errors.Wrapf(err, "get %s credentials", psmdbv1.RoleClusterAdmin)
	}

	mongo60, err := cluster.CompareMongoDBVersion("6.0")
	if err != nil {
		return stdoutBuf, stderrBuf, errors.Wrap(err, "compare mongo version")
	}

	mongoClient := "mongo"
	if mongo60 >= 0 {
		mongoClient = "mongosh"
	}

	cmd := []string{mongoClient, "--quiet", "-u", creds.Username, "-p", creds.Password, "--eval", eval}

	if err := r.clientcmd.Exec(ctx, pod, "mongod", cmd, nil, stdoutBuf, stderrBuf, false); err != nil {
		return stdoutBuf, stderrBuf, errors.Wrapf(err, "'%s' failed in %s (stdout: %s, stderr: %s)", eval, pod.Name, stdoutBuf.String(), stderrBuf.String())
	}
	log.V(1).Info("Cmd succeeded", "stdout", stdoutBuf.String(), "stderr", stderrBuf.String(), "pod", pod.Name, "eval", eval)

	return stdoutBuf, stderrBuf, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) runIsMaster(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB, pod *corev1.Pod) (bool, error) {
	stdoutBuf, _, err := r.runMongosh(ctx, cluster, pod, "db.hello().isWritablePrimary")
	if err != nil {
		return false, errors.Wrap(err, "run db.hello()")
	}

	return strings.TrimSuffix(stdoutBuf.String(), "\n") == "true", nil
}

func (r *ReconcilePerconaServerMongoDBRestore) stepDown(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB, pod *corev1.Pod) error {
	_, _, err := r.runMongosh(ctx, cluster, pod, "rs.stepDown()")
	if err != nil {
		return errors.Wrap(err, "run rs.stepDown()")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDBRestore) makePrimary(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB, pod *corev1.Pod, targetPod string) error {
	jsTempl := `cfg = rs.config(); podZero = cfg.members.find(member => member.tags.podName === "%s"); podZero.priority += 1; rs.reconfig(cfg)`

	_, _, err := r.runMongosh(ctx, cluster, pod, fmt.Sprintf(jsTempl, targetPod))
	if err != nil {
		return errors.Wrapf(err, "reconfigure replset in %s", pod.Name)
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDBRestore) prepareReplsetsForPhysicalRestore(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	replsets := cluster.Spec.Replsets
	if cluster.Spec.Sharding.Enabled {
		replsets = append(replsets, cluster.Spec.Sharding.ConfigsvrReplSet)
	}

	for _, rs := range replsets {
		log.Info("Preparing replset for physical restore", "replset", rs.Name)

		podList, err := psmdb.GetRSPods(ctx, r.client, cluster, rs.Name)
		if err != nil {
			return errors.Wrapf(err, "get pods of replset %s", rs.Name)
		}

		for _, pod := range podList.Items {
			isMaster, err := r.runIsMaster(ctx, cluster, &pod)
			if err != nil {
				log.V(1).Error(err, "failed to run db.hello()", "pod", pod.Name, "replset", rs.Name)
				continue
			}

			if !isMaster {
				log.V(1).Info("Skipping secondary pod", "pod", pod.Name, "replset", rs.Name)
				continue
			}

			podZero := rs.PodName(cluster, 0)

			if pod.Name == podZero {
				log.Info(fmt.Sprintf("%s is already primary", podZero), "replset", rs.Name)
				continue
			}

			log.Info(fmt.Sprintf("Current primary is %s", pod.Name), "pod", pod.Name, "replset", rs.Name)
			log.Info(fmt.Sprintf("Reconfiguring replset to make %s primary", podZero), "pod", pod.Name, "replset", rs.Name)

			if err = r.makePrimary(ctx, cluster, &pod, podZero); err != nil {
				return errors.Wrapf(err, "make %s primary", podZero)
			}

			log.Info("Stepping down the current primary", "primary", pod.Name, "pod", pod.Name, "replset", rs.Name)
			if err = r.stepDown(ctx, cluster, &pod); err != nil {
				return errors.Wrap(err, "step down")
			}
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDBRestore) checkIfReplsetsAreReadyForPhysicalRestore(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB) (bool, error) {
	log := logf.FromContext(ctx)

	replsets := cluster.Spec.Replsets
	if cluster.Spec.Sharding.Enabled {
		replsets = append(replsets, cluster.Spec.Sharding.ConfigsvrReplSet)
	}

	for _, rs := range replsets {
		log.Info("Checking if replset is ready for physical restore", "replset", rs.Name)

		podZero := rs.PodName(cluster, 0)

		pod := corev1.Pod{}
		if err := r.client.Get(ctx, types.NamespacedName{Name: podZero, Namespace: cluster.Namespace}, &pod); err != nil {
			return false, errors.Wrapf(err, "get pod %s", podZero)
		}

		isMaster, err := r.runIsMaster(ctx, cluster, &pod)
		if err != nil {
			return false, errors.Wrap(err, "check if pod zero is primary")
		}

		if !isMaster {
			log.Info(fmt.Sprintf("%s must be elected as primary before starting physical restore", pod.Name), "replset", rs.Name)
			return false, nil
		}

		log.Info("Replset is ready for physical restore", "replset", rs.Name, "primary", pod.Name)
	}

	return true, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) updatePBMConfigSecret(
	ctx context.Context,
	cr *psmdbv1.PerconaServerMongoDBRestore,
	cluster *psmdbv1.PerconaServerMongoDB,
	bcp *psmdbv1.PerconaServerMongoDBBackup,
) error {
	secret := corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{Name: r.pbmConfigName(cluster), Namespace: cluster.Namespace}, &secret)
	if client.IgnoreNotFound(err) != nil {
		return errors.Wrap(err, "get PBM config secret")
	}

	pbmC, err := backup.NewPBM(ctx, r.client, cluster)
	if err != nil {
		return errors.Wrap(err, "new PBM connection")
	}

	// PBM uses main storage to store restore metadata
	// regardless of backup storage. See PBM-1503.
	pbmConfig, err := pbmC.GetConfig(ctx)
	if err != nil {
		return errors.Wrap(err, "get PBM config")
	}

	pbmConfig.PITR.Enabled = false

	confBytes, err := yaml.Marshal(pbmConfig)
	if err != nil {
		return errors.Wrap(err, "marshal PBM config to yaml")
	}

	if bytes.Equal(confBytes, secret.Data["pbm_config.yaml"]) {
		return nil
	}

	secret = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.pbmConfigName(cluster),
			Namespace: cluster.Namespace,
			Labels:    naming.ClusterLabels(cluster),
		},
		Data: map[string][]byte{
			"pbm_config.yaml": confBytes,
		},
	}
	if cluster.CompareVersion("1.17.0") < 0 {
		secret.Labels = nil
	}

	if err := r.createOrUpdate(ctx, &secret); err != nil {
		return errors.Wrap(err, "create PBM config secret")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDBRestore) getReplsetPods(
	ctx context.Context,
	cluster *psmdbv1.PerconaServerMongoDB,
	rs *psmdbv1.ReplsetSpec,
	component string,
) (corev1.PodList, error) {
	mongodPods := corev1.PodList{}

	set := naming.RSLabels(cluster, rs)
	set[naming.LabelKubernetesComponent] = component

	err := r.client.List(ctx,
		&mongodPods,
		&client.ListOptions{
			Namespace:     cluster.Namespace,
			LabelSelector: labels.SelectorFromSet(set),
		},
	)

	return mongodPods, err
}

func (r *ReconcilePerconaServerMongoDBRestore) checkIfStatefulSetsAreReadyForPhysicalRestore(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB) (bool, error) {
	replsets := cluster.Spec.Replsets
	if cluster.Spec.Sharding.Enabled {
		replsets = append(replsets, cluster.Spec.Sharding.ConfigsvrReplSet)
	}

	for _, rs := range replsets {
		ready, err := r.checkStatefulSetForPhysicalRestore(ctx, cluster, rs, naming.ComponentMongod)
		if err != nil {
			return false, errors.Wrapf(err, "check %s %s statefulset", rs.Name, naming.ComponentMongod)
		}

		if !ready {
			return false, nil
		}

		if rs.NonVoting.Enabled {
			ready, err := r.checkStatefulSetForPhysicalRestore(ctx, cluster, rs, naming.ComponentNonVoting)
			if err != nil {
				return false, errors.Wrapf(err, "check %s %s statefulset", rs.Name, naming.ComponentNonVoting)
			}

			if !ready {
				return false, nil
			}
		}

		if rs.Hidden.Enabled {
			ready, err := r.checkStatefulSetForPhysicalRestore(ctx, cluster, rs, naming.ComponentHidden)
			if err != nil {
				return false, errors.Wrapf(err, "check %s %s statefulset", rs.Name, naming.ComponentHidden)
			}

			if !ready {
				return false, nil
			}
		}
	}

	return true, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) checkStatefulSetForPhysicalRestore(
	ctx context.Context,
	cluster *psmdbv1.PerconaServerMongoDB,
	rs *psmdbv1.ReplsetSpec,
	component string,
) (bool, error) {
	log := logf.FromContext(ctx)

	var stsName string
	switch component {
	case naming.ComponentMongod:
		stsName = naming.MongodStatefulSetName(cluster, rs)
	case naming.ComponentNonVoting:
		stsName = naming.NonVotingStatefulSetName(cluster, rs)
	case naming.ComponentHidden:
		stsName = naming.HiddenStatefulSetName(cluster, rs)
	}

	sts := appsv1.StatefulSet{}
	nn := types.NamespacedName{Namespace: cluster.Namespace, Name: stsName}
	err := r.client.Get(ctx, nn, &sts)
	if err != nil {
		return false, err
	}

	_, ok := sts.Annotations[psmdbv1.AnnotationRestoreInProgress]
	if !ok {
		return false, nil
	}

	if sts.Status.Replicas != sts.Status.ReadyReplicas {
		return false, nil
	}

	podList, err := r.getReplsetPods(ctx, cluster, rs, component)
	if err != nil {
		return false, errors.Wrapf(err, "get replset %s pods", rs.Name)
	}

	for _, pod := range podList.Items {
		if pod.ObjectMeta.Labels["controller-revision-hash"] != sts.Status.UpdateRevision {
			return false, nil
		}

		for _, c := range pod.Spec.Containers {
			if c.Name == naming.ContainerBackupAgent {
				return false, nil
			}
		}

		log.V(1).Info("Pod is ready for physical restore", "pod", pod.Name)
	}

	log.V(1).Info("Statefulset is ready for physical restore", "sts", sts.Name, "replset", rs.Name)

	return true, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) getLatestChunkTS(ctx context.Context, pod *corev1.Pod) (string, error) {
	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	container, pbmBinary := getPBMBinaryAndContainerForExec(pod)

	command := []string{pbmBinary, "status", "--out", "json"}
	if err := r.clientcmd.Exec(ctx, pod, container, command, nil, stdoutBuf, stderrBuf, false); err != nil {
		return "", errors.Wrapf(err, "get PBM status stderr: %s stdout: %s", stderrBuf.String(), stdoutBuf.String())
	}

	var pbmStatus struct {
		Backups struct {
			Chunks struct {
				Timelines []struct {
					Range struct {
						Start uint32 `json:"start"`
						End   uint32 `json:"end"`
					} `json:"range"`
				} `json:"pitrChunks"`
			} `json:"pitrChunks"`
		} `json:"backups"`
	}

	if err := json.Unmarshal(stdoutBuf.Bytes(), &pbmStatus); err != nil {
		return "", errors.Wrap(err, "unmarshal PBM status output")
	}

	if len(pbmStatus.Backups.Chunks.Timelines) < 1 {
		return "", errors.New("no oplog chunks")
	}

	latest := pbmStatus.Backups.Chunks.Timelines[len(pbmStatus.Backups.Chunks.Timelines)-1].Range.End
	ts := time.Unix(int64(latest), 0).UTC()

	return ts.Format("2006-01-02T15:04:05"), nil
}

func (r *ReconcilePerconaServerMongoDBRestore) disablePITR(ctx context.Context, pod *corev1.Pod) error {
	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	container, pbmBinary := getPBMBinaryAndContainerForExec(pod)

	command := []string{pbmBinary, "config", "--set", "pitr.enabled=false"}
	if err := r.clientcmd.Exec(ctx, pod, container, command, nil, stdoutBuf, stderrBuf, false); err != nil {
		return errors.Wrapf(err, "disable PiTR stderr: %s stdout: %s", stderrBuf.String(), stdoutBuf.String())
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDBRestore) pbmConfigName(cluster *psmdbv1.PerconaServerMongoDB) string {
	if cluster.CompareVersion("1.16.0") < 0 {
		return "pbm-config"
	}
	return cluster.Name + "-pbm-config"
}

func (r *ReconcilePerconaServerMongoDBRestore) waitForPBMOperationsToFinish(ctx context.Context, pod *corev1.Pod) error {
	log := logf.FromContext(ctx)

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	container, pbmBinary := getPBMBinaryAndContainerForExec(pod)

	waitErr := errors.New("waiting for PBM operation to finish")
	err := retry.OnError(wait.Backoff{
		Duration: 5 * time.Second,
		Factor:   2.0,
		Cap:      time.Hour,
		Steps:    12,
	}, func(err error) bool { return err == waitErr }, func() error {
		err := retry.OnError(retry.DefaultBackoff, func(err error) bool { return strings.Contains(err.Error(), "No agent available") }, func() error {
			stdoutBuf.Reset()
			stderrBuf.Reset()

			command := []string{pbmBinary, "status", "--out", "json"}
			err := r.clientcmd.Exec(ctx, pod, container, command, nil, stdoutBuf, stderrBuf, false)
			if err != nil {
				log.Error(err, "failed to get PBM status")
				return err
			}

			log.V(1).Info("PBM status", "status", stdoutBuf.String())

			return nil
		})
		if err != nil {
			return errors.Wrapf(err, "get PBM status stderr: %s stdout: %s", stderrBuf.String(), stdoutBuf.String())
		}

		var pbmStatus struct {
			Running struct {
				Type string `json:"type,omitempty"`
				OpId string `json:"opID,omitempty"`
			} `json:"running"`
		}

		if err := json.Unmarshal(stdoutBuf.Bytes(), &pbmStatus); err != nil {
			return errors.Wrap(err, "unmarshal PBM status output")
		}

		if len(pbmStatus.Running.OpId) == 0 {
			return nil
		}

		log.Info("Waiting for another PBM operation to finish", "type", pbmStatus.Running.Type, "opID", pbmStatus.Running.OpId)

		return waitErr
	})
	if err != nil {
		return err
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDBRestore) checkIfPBMAgentsReadyForPhysicalRestore(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB) (bool, error) {
	log := logf.FromContext(ctx)

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	err := retry.OnError(anotherOpBackoff, func(err error) bool {
		return strings.Contains(err.Error(), "unable to upgrade connection")
	}, func() error {
		stdoutBuf.Reset()
		stderrBuf.Reset()

		pod := corev1.Pod{}
		nn := types.NamespacedName{Name: cluster.Spec.Replsets[0].PodName(cluster, 0), Namespace: cluster.Namespace}
		if err := r.client.Get(ctx, nn, &pod); err != nil {
			return errors.Wrap(err, "get pod")
		}

		container, pbmBinary := getPBMBinaryAndContainerForExec(&pod)

		command := []string{pbmBinary, "status", "-s", "cluster", "--out", "json"}
		err := r.clientcmd.Exec(ctx, &pod, container, command, nil, stdoutBuf, stderrBuf, false)
		if err != nil {
			return errors.Wrap(err, "get pbm status")
		}

		return nil
	})
	if err != nil {
		return false, err
	}

	var pbmStatus struct {
		Cluster []struct {
			Name  string `json:"rs"`
			Nodes []struct {
				Host string `json:"host"`
				Role string `json:"role"`
				Ok   bool   `json:"ok"`
			}
		} `json:"cluster"`
	}

	if err := json.Unmarshal(stdoutBuf.Bytes(), &pbmStatus); err != nil {
		return false, errors.Wrap(err, "unmarshal PBM status output")
	}

	for _, replset := range pbmStatus.Cluster {
		for _, node := range replset.Nodes {
			if node.Role == "A" { // arbiter
				continue
			}

			if !node.Ok {
				log.Info("pbm-agent is not ready", "replset", replset.Name, "host", node.Host)
				return false, nil
			}
		}
	}

	return true, nil
}

func getPBMBinaryAndContainerForExec(pod *corev1.Pod) (string, string) {
	container := "mongod"
	pbmBinary := "/opt/percona/pbm"

	for _, c := range pod.Spec.Containers {
		if c.Name == naming.ContainerBackupAgent {
			return naming.ContainerBackupAgent, "pbm"
		}
	}

	return container, pbmBinary
}
