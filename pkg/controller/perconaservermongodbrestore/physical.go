package perconaservermongodbrestore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	psmdbInit "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/init"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
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

	replsets := cluster.GetAllReplsets()

	if cr.Status.State == psmdbv1.RestoreStateNew {
		pod, group, err := r.restorePod(ctx, cluster, replsets[0])
		if err != nil {
			return status, err
		}

		if err := retry.OnError(anotherOpBackoff, func(err error) bool {
			return strings.Contains(err.Error(), "another operation")
		}, func() error {
			return r.disablePITR(ctx, pod, group.ContainerName)
		}); err != nil {
			return status, errors.Wrap(err, "disable pitr")
		}

		if cr.Spec.PITR != nil {
			var ts string
			switch cr.Spec.PITR.Type {
			case psmdbv1.PITRestoreTypeDate:
				ts = cr.Spec.PITR.Date.Format("2006-01-02T15:04:05")
			case psmdbv1.PITRestoreTypeLatest:
				ts, err = r.getLatestChunkTS(ctx, cr, cluster)
				if err != nil {
					return status, errors.Wrap(err, "get latest chunk timestamp")
				}
			}

			status.PITRTarget = ts
		}

		if err := r.updatePBMConfigSecret(ctx, cluster); err != nil {
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

		pod, group, err := r.restorePod(ctx, cluster, rs)
		if err != nil {
			return status, err
		}

		var restoreCommand []string
		if cr.Spec.PITR != nil {
			restoreCommand = []string{
				"/opt/percona/pbm", "restore",
				"--base-snapshot", bcp.Status.PBMname,
				"--time", cr.Status.PITRTarget,
				"--out", "json",
			}
		} else {
			restoreCommand = []string{
				"/opt/percona/pbm", "restore",
				bcp.Status.PBMname,
				"--out", "json",
			}
		}

		if cmp, err := cluster.ComparePBMAgentVersion("2.14.0"); err == nil && cmp >= 0 {
			restoreCommand = append(restoreCommand, "--yes")
		}

		if cr.Spec.RSMap != nil {
			var rsMap []string
			for k, v := range cr.Spec.RSMap {
				rsMap = append(rsMap, fmt.Sprintf("%s=%s", v, k))
			}
			restoreCommand = append(restoreCommand, "--replset-remapping", strings.Join(rsMap, ","))
		}

		err = retry.OnError(anotherOpBackoff, func(err error) bool {
			return strings.Contains(err.Error(), "another operation") ||
				strings.Contains(err.Error(), "unable to upgrade connection")
		}, func() error {
			log.Info("Starting restore", "command", restoreCommand, "pod", pod.Name)

			stdoutBuf.Reset()
			stderrBuf.Reset()

			err := r.clientcmd.Exec(ctx, pod, group.ContainerName, restoreCommand, nil, stdoutBuf, stderrBuf, false)
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
			return status, errors.Wrapf(err, "unmarshal PBM restore output: %s", stdoutBuf.String())
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

		pod, group, err := r.restorePod(ctx, cluster, replsets[0])
		if err != nil {
			return err
		}

		log.V(1).Info("Check restore status", "command", command, "pod", pod.Name)

		if err := r.clientcmd.Exec(ctx, pod, group.ContainerName, command, nil, stdoutBuf, stderrBuf, false); err != nil {
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
			set, err := membergroup.Resolve(cluster, rs)
			if err != nil {
				return status, errors.Wrapf(err, "resolve member groups for replset %s", rs.Name)
			}

			toDelete := set.GetStatefulSetNames()

			if cluster.IsSearchEnabled() && rs.ClusterRole != api.ClusterRoleConfigSvr {
				toDelete = append(toDelete, naming.SearchStatefulSetName(cluster, rs))
			}

			for _, stsName := range toDelete {
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

func (r *ReconcilePerconaServerMongoDBRestore) prepareStatefulSetForPhysicalRestore(
	ctx context.Context,
	cluster *psmdbv1.PerconaServerMongoDB,
	sts *appsv1.StatefulSet,
	group membergroup.Group,
	port int32,
) error {
	log := logf.FromContext(ctx)

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
	pbmInit := psmdbInit.EntrypointContainer(
		cluster,
		"pbm-init",
		cluster.Spec.Backup.Image,
		cluster.Spec.ImagePullPolicy,
		cmd,
	)
	if cluster.CompareVersion("1.23.0") >= 0 && cluster.Spec.InitContainerSecurityContext != nil {
		pbmInit.SecurityContext = cluster.Spec.InitContainerSecurityContext
	}
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

	containerIdx := -1
	for idx, c := range sts.Spec.Template.Spec.Containers {
		if c.Name == group.ContainerName {
			containerIdx = idx
			break
		}
	}
	if containerIdx == -1 {
		return errors.Errorf("container %s not found in statefulset %s", group.ContainerName, sts.Name)
	}

	sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: "pbm-config",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: r.pbmConfigName(cluster),
			},
		},
	})
	sts.Spec.Template.Spec.Containers[containerIdx].VolumeMounts = append(sts.Spec.Template.Spec.Containers[containerIdx].VolumeMounts, corev1.VolumeMount{
		Name:      "pbm-config",
		MountPath: "/etc/pbm/",
		ReadOnly:  true,
	})
	sts.Spec.Template.Spec.Containers[containerIdx].VolumeMounts = append(sts.Spec.Template.Spec.Containers[containerIdx].VolumeMounts, cluster.Spec.Backup.VolumeMounts...)
	sts.Spec.Template.Spec.Containers[containerIdx].Command = []string{"/opt/percona/physical-restore-ps-entry.sh"}

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
	sts.Spec.Template.Spec.Containers[containerIdx].Env = append(sts.Spec.Template.Spec.Containers[containerIdx].Env, pbmEnvVars...)

	if cluster.CompareVersion("1.23.0") >= 0 && psmdb.ShouldSetAWSSDKChecksumEnvVars(cluster) {
		sts.Spec.Template.Spec.Containers[containerIdx].Env = append(sts.Spec.Template.Spec.Containers[containerIdx].Env, psmdb.AWSSDKChecksumEnvVars()...)
	}
	if cluster.CompareVersion("1.23.0") >= 0 && psmdb.ShouldSetOCIResourcePrincipalEnvVars(cluster) {
		sts.Spec.Template.Spec.Containers[containerIdx].Env = append(sts.Spec.Template.Spec.Containers[containerIdx].Env, psmdb.OCIResourcePrincipalEnvVars(cluster)...)
	}

	sslSecret := new(corev1.Secret)
	err := r.client.Get(ctx, types.NamespacedName{Name: api.SSLSecretName(cluster), Namespace: cluster.Namespace}, sslSecret)
	if client.IgnoreNotFound(err) != nil {
		return errors.Wrap(err, "check ssl secrets")
	}

	mongoDBURI := "mongodb://$(PBM_AGENT_MONGODB_USERNAME):$(PBM_AGENT_MONGODB_PASSWORD)@$(POD_NAME)"
	if cluster.CompareVersion("1.21.0") >= 0 {
		mongoDBURI = psmdb.BuildMongoDBURI(ctx, cluster.TLSEnabled(), sslSecret)

		sts.Spec.Template.Spec.Containers[containerIdx].Env = append(sts.Spec.Template.Spec.Containers[containerIdx].Env, []corev1.EnvVar{
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

	sts.Spec.Template.Spec.Containers[containerIdx].Env = append(sts.Spec.Template.Spec.Containers[containerIdx].Env, []corev1.EnvVar{
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

	// During physical restore the backup-agent container is removed and mongod takes
	// over PBM operations directly. Add SSL_CERT_FILE and CA volume mounts so mongod
	// can verify the MinIO TLS certificate when caBundle is configured.
	if cluster.CompareVersion("1.23.0") >= 0 {
		cas := psmdb.CollectStorageCABundles(cluster)
		if len(cas) > 0 {
			sts.Spec.Template.Spec.Containers[containerIdx].VolumeMounts = append(
				sts.Spec.Template.Spec.Containers[containerIdx].VolumeMounts,
				psmdb.GetCAVolumeMounts()...,
			)
			sts.Spec.Template.Spec.Containers[containerIdx].Env = append(
				sts.Spec.Template.Spec.Containers[containerIdx].Env,
				corev1.EnvVar{
					Name:  "SSL_CERT_FILE",
					Value: path.Join(naming.BackupStorageCAFileDirectory, naming.BackupStorageCAFileName),
				},
			)
		}
	}

	err = r.client.Update(ctx, sts)
	if err != nil {
		return err
	}

	log.Info("Updated statefulset", "name", sts.Name)
	return nil
}

func (r *ReconcilePerconaServerMongoDBRestore) prepareStatefulSetsForPhysicalRestore(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	replsets := cluster.GetAllReplsets()
	for _, rs := range replsets {
		set, err := membergroup.Resolve(cluster, rs)
		if err != nil {
			return errors.Wrapf(err, "resolve member groups for replset %s", rs.Name)
		}

		for _, group := range set.GetAll() {
			sts := appsv1.StatefulSet{}
			nn := types.NamespacedName{Namespace: cluster.Namespace, Name: group.STSName}
			if err := r.client.Get(ctx, nn, &sts); err != nil {
				if k8serrors.IsNotFound(err) {
					continue
				}
				return err
			}

			_, ok := sts.Annotations[psmdbv1.AnnotationRestoreInProgress]
			if ok {
				continue
			}

			log.Info("Preparing statefulset for physical restore", "name", group.STSName)

			if !group.DataBearing {
				if err := r.pauseStatefulSetForPhysicalRestore(ctx, nn); err != nil {
					return errors.Wrapf(err, "pause statefulset %s for physical restore", group.STSName)
				}
				continue
			}

			err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				sts := appsv1.StatefulSet{}
				if err := r.client.Get(ctx, nn, &sts); err != nil {
					return err
				}
				return r.prepareStatefulSetForPhysicalRestore(ctx, cluster, &sts, group, rs.GetPort())
			})
			if err != nil {
				return errors.Wrapf(err, "prepare statefulset %s for physical restore", group.STSName)
			}
		}

		if cluster.IsSearchEnabled() {
			stsName := naming.SearchStatefulSetName(cluster, rs)
			nn := types.NamespacedName{Namespace: cluster.Namespace, Name: stsName}

			log.Info("Preparing statefulset for physical restore", "name", stsName)

			if err := r.pauseStatefulSetForPhysicalRestore(ctx, nn); err != nil {
				return errors.Wrapf(err, "pause statefulset %s for physical restore", stsName)
			}
		}
	}

	return nil
}

// pauseStatefulSetForPhysicalRestore scales a workload to zero and marks it as
// taking part in the restore.
func (r *ReconcilePerconaServerMongoDBRestore) pauseStatefulSetForPhysicalRestore(
	ctx context.Context,
	nn types.NamespacedName,
) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		sts := appsv1.StatefulSet{}
		if err := r.client.Get(ctx, nn, &sts); err != nil {
			return err
		}

		orig := sts.DeepCopy()

		sts.Spec.Replicas = new(int32(0))

		if sts.Annotations == nil {
			sts.Annotations = make(map[string]string)
		}
		sts.Annotations[psmdbv1.AnnotationRestoreInProgress] = "true"

		return r.client.Patch(ctx, &sts, client.MergeFrom(orig))
	})
}

// workaround: marshalUnsafe is used to marshal PBM config to yaml when the storage credentials are needed.
// PBM masks the storage credentials when masking to YAML/JSON, but they are preserved in BSON.
func yamlMarshalUnsafe(in any) ([]byte, error) {
	bsonBytes, err := bson.Marshal(in)
	if err != nil {
		return nil, errors.Wrap(err, "marshal to bson")
	}

	// DefaultDocumentM is required so that nested documents decode into bson.M as
	// well. Without it, the mongo-driver v2 decoder decodes nested documents into
	// bson.D, which yaml.Marshal then renders as a list of {key, value} pairs
	// instead of a proper YAML map.
	dec := bson.NewDecoder(bson.NewDocumentReader(bytes.NewReader(bsonBytes)))
	dec.DefaultDocumentM()

	var tmp bson.M
	if err := dec.Decode(&tmp); err != nil {
		return nil, errors.Wrap(err, "unmarshal to bson.M")
	}
	delete(tmp, "epoch")
	delete(tmp, "_id")

	yamlBytes, err := yaml.Marshal(tmp)
	if err != nil {
		return nil, errors.Wrap(err, "marshal to yaml")
	}

	return yamlBytes, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) updatePBMConfigSecret(
	ctx context.Context,
	cluster *psmdbv1.PerconaServerMongoDB,
) error {
	log := logf.FromContext(ctx)

	secret := corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{Name: r.pbmConfigName(cluster), Namespace: cluster.Namespace}, &secret)
	if client.IgnoreNotFound(err) != nil {
		return errors.Wrap(err, "get PBM config secret")
	}

	currentConf, err := r.getPBMConfigFromPod(ctx, cluster)
	if err != nil {
		return errors.Wrap(err, "get current pbm config from pod")
	}

	pbmC, err := r.newPBMFunc(ctx, r.client, cluster)
	if err != nil {
		return errors.Wrap(err, "new PBM connection")
	}
	defer func() {
		if err := pbmC.Close(ctx); err != nil {
			log.Error(err, "failed to close PBM connection")
		}
	}()

	// PBM uses main storage to store restore metadata
	// regardless of backup storage. See PBM-1503.
	pbmConfig, err := pbmC.GetConfig(ctx)
	if err != nil {
		return errors.Wrap(err, "get PBM config")
	}

	if pbmConfig.PITR != nil {
		pbmConfig.PITR.Enabled = false
	}

	newConfBytes, err := yamlMarshalUnsafe(pbmConfig)
	if err != nil {
		return errors.Wrap(err, "marshal PBM config to yaml")
	}

	newConf := make(map[string]any)
	if err := yaml.Unmarshal(newConfBytes, newConf); err != nil {
		return errors.Wrap(err, "unmarshal new PBM config to map")
	}

	// we need to do this to not break physical restores if PBM go module version
	// differs from pbm-agent version in the running cluster. only known pbm config
	// fields will be updated.
	desiredConfBytes, err := yaml.Marshal(updateKnownFields(currentConf, newConf))
	if err != nil {
		return errors.Wrap(err, "marshal desired conf")
	}

	if bytes.Equal(desiredConfBytes, secret.Data["pbm_config.yaml"]) {
		return nil
	}

	secret = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.pbmConfigName(cluster),
			Namespace: cluster.Namespace,
			Labels:    naming.ClusterLabels(cluster),
		},
		Data: map[string][]byte{
			"pbm_config.yaml": desiredConfBytes,
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

func updateKnownFields(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a))
	maps.Copy(out, a)
	for k, v := range b {
		// Ignore any field in b that does not exist in a.
		bv, ok := out[k]
		if !ok {
			continue
		}
		if v, ok := v.(map[string]any); ok {
			if bv, ok := bv.(map[string]any); ok {
				out[k] = updateKnownFields(bv, v)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func (r *ReconcilePerconaServerMongoDBRestore) getPBMConfigFromPod(
	ctx context.Context,
	cluster *psmdbv1.PerconaServerMongoDB,
) (map[string]any, error) {
	conf := make(map[string]any)

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	pod, group, err := r.restorePod(ctx, cluster, cluster.GetAllReplsets()[0])
	if err != nil {
		return conf, err
	}

	container, pbmBinary := getPBMBinaryAndContainerForExec(pod, group.ContainerName)

	command := []string{pbmBinary, "config"}
	err = r.clientcmd.Exec(ctx, pod, container, command, nil, stdoutBuf, stderrBuf, false)
	if err != nil {
		return conf, errors.Wrap(err, "get pbm config")
	}

	if err := yaml.Unmarshal(stdoutBuf.Bytes(), &conf); err != nil {
		return conf, errors.Wrap(err, "unmarshal pbm config output")
	}

	return conf, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) checkIfStatefulSetsAreReadyForPhysicalRestore(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB) (bool, error) {
	replsets := cluster.Spec.Replsets
	if cluster.Spec.Sharding.Enabled {
		replsets = append(replsets, cluster.Spec.Sharding.ConfigsvrReplSet)
	}

	for _, rs := range replsets {
		groups, err := r.restoreGroups(ctx, cluster, rs)
		if err != nil {
			return false, err
		}

		for _, group := range groups {
			ready, err := r.checkStatefulSetForPhysicalRestore(ctx, cluster, rs, group)
			if err != nil {
				return false, errors.Wrapf(err, "check statefulset %s", group.STSName)
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
	group membergroup.Group,
) (bool, error) {
	log := logf.FromContext(ctx)

	sts := appsv1.StatefulSet{}
	nn := types.NamespacedName{Namespace: cluster.Namespace, Name: group.STSName}
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

	podList, err := psmdb.GetGroupPods(ctx, r.client, cluster, rs, group)
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

func (r *ReconcilePerconaServerMongoDBRestore) getLatestChunkTS(
	ctx context.Context,
	cr *psmdbv1.PerconaServerMongoDBRestore,
	cluster *psmdbv1.PerconaServerMongoDB,
) (string, error) {
	pbmc, err := r.newPBMFunc(ctx, r.client, cluster)
	if err != nil {
		return "", errors.Wrap(err, "new PBM connection")
	}
	defer pbmc.Close(ctx) //nolint:errcheck

	timeline, err := pbmc.GetLatestTimelinePITR(ctx, cr.Spec.RSMap)
	if err != nil {
		return "", errors.Wrap(err, "get latest timeline")
	}

	ts := time.Unix(int64(timeline.End), 0).UTC()
	return ts.Format("2006-01-02T15:04:05"), nil
}

func (r *ReconcilePerconaServerMongoDBRestore) disablePITR(ctx context.Context, pod *corev1.Pod, fallbackContainer string) error {
	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	container, pbmBinary := getPBMBinaryAndContainerForExec(pod, fallbackContainer)

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

func (r *ReconcilePerconaServerMongoDBRestore) checkIfPBMAgentsReadyForPhysicalRestore(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB) (bool, error) {
	log := logf.FromContext(ctx)

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	err := retry.OnError(anotherOpBackoff, func(err error) bool {
		return strings.Contains(err.Error(), "unable to upgrade connection")
	}, func() error {
		stdoutBuf.Reset()
		stderrBuf.Reset()

		pod, group, err := r.restorePod(ctx, cluster, cluster.Spec.Replsets[0])
		if err != nil {
			return err
		}

		container, pbmBinary := getPBMBinaryAndContainerForExec(pod, group.ContainerName)

		command := []string{pbmBinary, "status", "-s", "cluster", "--out", "json"}
		err = r.clientcmd.Exec(ctx, pod, container, command, nil, stdoutBuf, stderrBuf, false)
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

func getPBMBinaryAndContainerForExec(pod *corev1.Pod, fallbackContainer string) (string, string) {
	for _, c := range pod.Spec.Containers {
		if c.Name == naming.ContainerBackupAgent {
			return naming.ContainerBackupAgent, "pbm"
		}
	}

	return fallbackContainer, "/opt/percona/pbm"
}
