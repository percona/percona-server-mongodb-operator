package perconaservermongodbrestore

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/version"
)

func (r *ReconcilePerconaServerMongoDBRestore) reconcilePhysicalRestore(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBRestore, bcp *psmdbv1.PerconaServerMongoDBBackup) (psmdbv1.PerconaServerMongoDBRestoreStatus, error) {
	log := logf.FromContext(ctx)

	status := cr.Status

	cluster := &psmdbv1.PerconaServerMongoDB{}
	err := r.client.Get(ctx, types.NamespacedName{Name: cr.Spec.ClusterName, Namespace: cr.Namespace}, cluster)
	if err != nil {
		return status, errors.Wrapf(err, "get cluster %s/%s", cr.Namespace, cr.Spec.ClusterName)
	}

	if cluster.Spec.Unmanaged {
		return status, errors.New("cluster is unmanaged")
	}

	svr, err := version.Server()
	if err != nil {
		return status, errors.Wrapf(err, "fetch server version")
	}

	if err := cluster.CheckNSetDefaults(svr.Platform, log); err != nil {
		return status, errors.Wrapf(err, "set defaults for %s/%s", cluster.Namespace, cluster.Name)
	}

	for _, rs := range cluster.Spec.Replsets {
		if rs.Arbiter.Enabled {
			return status, errors.New("physical restores are not supported for deployments with arbiter nodes")
		}
	}

	if cr.Status.State == psmdbv1.RestoreStateNew {
		pbmConf := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pbm-config",
				Namespace: cluster.Namespace,
			},
		}
		if err := r.client.Delete(ctx, &pbmConf); err != nil && !k8serrors.IsNotFound(err) {
			return status, errors.Wrapf(err, "delete secret pbm-config")
		}

		status.State = psmdbv1.RestoreStateWaiting
	}

	if err := r.createPBMConfigSecret(ctx, cr, cluster, bcp); err != nil {
		return status, errors.Wrap(err, "create PBM config secret")
	}

	if err := r.prepareStatefulSetsForPhysicalRestore(ctx, cluster); err != nil {
		return status, errors.Wrap(err, "prepare statefulsets for physical restore")
	}

	ready, err := r.checkIfStatefulSetsAreReadyForPhysicalRestore(ctx, cluster)
	if err != nil {
		return status, errors.Wrap(err, "check if statefulsets are ready for physical restore")
	}

	if (!ready && cr.Status.State != psmdbv1.RestoreStateRunning) || cr.Status.State == psmdbv1.RestoreStateNew {
		log.Info("Waiting for statefulsets to be ready before restore", "ready", ready)
		return status, nil
	}

	pod := corev1.Pod{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: cluster.Name + "-" + cluster.Spec.Replsets[0].Name + "-0", Namespace: cluster.Namespace}, &pod); err != nil {
		return status, errors.Wrap(err, "get pod")
	}

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	if cr.Status.State == psmdbv1.RestoreStateWaiting {
		command := []string{"/opt/percona/pbm", "config", "--file", "/etc/pbm/pbm_config.yaml"}
		log.Info("Set PBM configuration", "command", command)
		if err := r.clientcmd.Exec(&pod, "mongod", command, nil, stdoutBuf, stderrBuf, false); err != nil {
			return status, errors.Wrapf(err, "resync config stderr: %s stdout: %s", stderrBuf.String(), stdoutBuf.String())
		}

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		timeout := time.NewTimer(900 * time.Second)
		defer timeout.Stop()

	outer:
		for {
			select {
			case <-timeout.C:
				return status, errors.Errorf("timeout while waiting PBM operation to finish")
			case <-ticker.C:
				err := retry.OnError(retry.DefaultBackoff, func(err error) bool { return strings.Contains(err.Error(), "No agent available") }, func() error {
					stdoutBuf.Reset()
					stderrBuf.Reset()

					command = []string{"/opt/percona/pbm", "status", "--out", "json"}
					if err := r.clientcmd.Exec(&pod, "mongod", command, nil, stdoutBuf, stderrBuf, false); err != nil {
						return errors.Wrapf(err, "get PBM status stderr: %s stdout: %s", stderrBuf.String(), stdoutBuf.String())
					}

					return nil
				})
				if err != nil {
					return status, err
				}

				var pbmStatus struct {
					Running struct {
						Type string `json:"type,omitempty"`
						OpId string `json:"opID,omitempty"`
					} `json:"running"`
				}

				if err := json.Unmarshal(stdoutBuf.Bytes(), &pbmStatus); err != nil {
					return status, errors.Wrap(err, "unmarshal PBM status output")
				}

				if len(pbmStatus.Running.OpId) == 0 {
					break outer
				}

				log.Info("Waiting for another PBM operation to finish", "type", pbmStatus.Running.Type, "opID", pbmStatus.Running.OpId)
			}
		}

		command = []string{"/opt/percona/pbm", "restore", bcp.Status.PBMname, "--out", "json"}
		log.Info("Starting restore", "command", command)

		stdoutBuf.Reset()
		stderrBuf.Reset()
		err := r.clientcmd.Exec(&pod, "mongod", command, nil, stdoutBuf, stderrBuf, false)
		if err != nil {
			return status, errors.Wrapf(err, "start restore stderr: %s stdout: %s", stderrBuf.String(), stdoutBuf.String())
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

	meta := pbm.BackupMeta{}

	err = retry.OnError(retry.DefaultBackoff, func(err error) bool { return strings.Contains(err.Error(), "container is not created or running") }, func() error {
		stdoutBuf.Reset()
		stderrBuf.Reset()

		command := []string{
			"/opt/percona/pbm", "describe-restore", cr.Status.PBMname,
			"--config", "/etc/pbm/pbm_config.yaml",
			"--out", "json",
		}

		log.V(1).Info("Check restore status", "command", command)
		if err := r.clientcmd.Exec(&pod, "mongod", command, nil, stdoutBuf, stderrBuf, false); err != nil {
			return errors.Wrap(err, "describe restore")
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
	case pbm.StatusStarting:
		for _, rs := range meta.Replsets {
			if rs.Status == pbm.StatusRunning {
				status.State = psmdbv1.RestoreStateRunning
				return status, nil
			}
		}
	case pbm.StatusRunning:
		status.State = psmdbv1.RestoreStateRunning
	case pbm.StatusDone:
		for _, rs := range meta.Replsets {
			if rs.Status == pbm.StatusDone {
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
			stsName := cluster.Name + "-" + rs.Name

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

		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			c := &psmdbv1.PerconaServerMongoDB{}
			err := r.client.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: cluster.Namespace}, c)
			if err != nil {
				return err
			}

			orig := c.DeepCopy()
			c.Annotations[psmdbv1.AnnotationResyncPBM] = "true"

			return r.client.Patch(ctx, c, client.MergeFrom(orig))
		})
		if err != nil {
			return status, errors.Wrapf(err, "annotate psmdb/%s for PBM resync", cluster.Name)
		}

	}

	return status, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) prepareStatefulSetsForPhysicalRestore(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	replsets := cluster.Spec.Replsets
	if cluster.Spec.Sharding.Enabled {
		replsets = append(replsets, cluster.Spec.Sharding.ConfigsvrReplSet)
	}

	for _, rs := range replsets {
		stsName := cluster.Name + "-" + rs.Name

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

		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			sts := appsv1.StatefulSet{}
			nn := types.NamespacedName{Namespace: cluster.Namespace, Name: stsName}
			err := r.client.Get(ctx, nn, &sts)
			if err != nil {
				return err
			}

			// Annotating statefulset to stop reconciliation in psmdb_controller
			sts.Annotations[psmdbv1.AnnotationRestoreInProgress] = "true"

			cmd := []string{
				"bash", "-c",
				"install -D /usr/bin/pbm /opt/percona/pbm && install -D /usr/bin/pbm-agent /opt/percona/pbm-agent",
			}
			pbmInit := psmdb.EntrypointInitContainer(
				cluster,
				"pbm-init",
				cluster.Spec.Backup.Image,
				cluster.Spec.ImagePullPolicy,
				cmd,
			)
			sts.Spec.Template.Spec.InitContainers = append(sts.Spec.Template.Spec.InitContainers, pbmInit)

			// remove backup-agent container
			pbmIdx := -1
			for idx, c := range sts.Spec.Template.Spec.Containers {
				if c.Name == "backup-agent" {
					pbmIdx = idx
					break
				}
			}
			if pbmIdx == -1 {
				return errors.New("failed to find backup-agent container")
			}
			sts.Spec.Template.Spec.Containers = append(sts.Spec.Template.Spec.Containers[:pbmIdx], sts.Spec.Template.Spec.Containers[pbmIdx+1:]...)

			sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes, corev1.Volume{
				Name: "pbm-config",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "pbm-config",
					},
				},
			})
			sts.Spec.Template.Spec.Containers[0].VolumeMounts = append(sts.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
				Name:      "pbm-config",
				MountPath: "/etc/pbm/",
				ReadOnly:  true,
			})
			sts.Spec.Template.Spec.Containers[0].Command = []string{"/opt/percona/physical-restore-ps-entry.sh"}
			sts.Spec.Template.Spec.Containers[0].Env = append(sts.Spec.Template.Spec.Containers[0].Env, []corev1.EnvVar{
				{
					Name: "PBM_AGENT_MONGODB_USERNAME",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							Key: "MONGODB_BACKUP_USER",
							LocalObjectReference: corev1.LocalObjectReference{
								Name: cluster.Spec.Secrets.Users,
							},
						},
					},
				},
				{
					Name: "PBM_AGENT_MONGODB_PASSWORD",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							Key: "MONGODB_BACKUP_PASSWORD",
							LocalObjectReference: corev1.LocalObjectReference{
								Name: cluster.Spec.Secrets.Users,
							},
						},
					},
				},
				{
					Name: "POD_NAME",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "metadata.name",
						},
					},
				},
				{
					Name:  "PBM_MONGODB_URI",
					Value: "mongodb://$(PBM_AGENT_MONGODB_USERNAME):$(PBM_AGENT_MONGODB_PASSWORD)@$(POD_NAME)",
				},
			}...)

			err = r.client.Update(ctx, &sts)
			if err != nil {
				return err
			}

			log.Info("Updated statefulset", "name", stsName)
			return nil
		})
		if err != nil {
			return errors.Wrapf(err, "prepare statefulset %s for physical restore", stsName)
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDBRestore) createPBMConfigSecret(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBRestore, cluster *psmdbv1.PerconaServerMongoDB, bcp *psmdbv1.PerconaServerMongoDBBackup) error {
	log := logf.FromContext(ctx)

	secret := corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{Name: "pbm-config", Namespace: cluster.Namespace}, &secret)
	if err == nil {
		return nil
	} else if !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "get PBM config secret")
	}

	log.V(1).Info("Configuring PBM", "storage", bcp.Spec.StorageName)

	storage, err := r.getStorage(cr, cluster, bcp.Spec.StorageName)
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	pbmConfig, err := backup.GetPBMConfig(ctx, r.client, cluster, storage)
	if err != nil {
		return errors.Wrap(err, "get PBM config")
	}

	confBytes, err := yaml.Marshal(pbmConfig)
	if err != nil {
		return errors.Wrap(err, "marshal PBM config to yaml")
	}

	secret = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pbm-config",
			Namespace: cluster.Namespace,
		},
		Data: map[string][]byte{
			"pbm_config.yaml": confBytes,
		},
	}
	if err := r.client.Create(ctx, &secret); err != nil {
		return errors.Wrap(err, "create PBM config secret")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDBRestore) getReplsetPods(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB, rs *psmdbv1.ReplsetSpec) (corev1.PodList, error) {
	mongodPods := corev1.PodList{}

	set := psmdbv1.MongodLabels(cluster)
	set["app.kubernetes.io/replset"] = rs.Name

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
		stsName := cluster.Name + "-" + rs.Name

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

		podList, err := r.getReplsetPods(ctx, cluster, rs)
		if err != nil {
			return false, errors.Wrapf(err, "get replset %s pods", rs.Name)
		}

		for _, pod := range podList.Items {
			if pod.ObjectMeta.Labels["controller-revision-hash"] != sts.Status.UpdateRevision {
				return false, nil
			}

			for _, c := range pod.Spec.Containers {
				if c.Name == "backup-agent" {
					return false, nil
				}
			}
		}
	}

	return true, nil
}
