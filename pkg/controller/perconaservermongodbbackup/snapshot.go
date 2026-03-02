package perconaservermongodbbackup

import (
	"context"
	"fmt"
	"strings"
	"time"

	volumesnapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	pbmErrors "github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

type snapshotBackups struct {
	pbm  backup.PBM
	spec api.BackupSpec
}

func (r *ReconcilePerconaServerMongoDBBackup) newSnapshotBackups(ctx context.Context, cluster *api.PerconaServerMongoDB) (*snapshotBackups, error) {
	if cluster == nil {
		return &snapshotBackups{}, nil
	}
	cn, err := r.newPBMFunc(ctx, r.client, cluster)
	if err != nil {
		return nil, errors.Wrap(err, "reate pbm object")
	}

	return &snapshotBackups{pbm: cn, spec: cluster.Spec.Backup}, nil
}

func (b *snapshotBackups) PBM() backup.PBM {
	return b.pbm
}

func (b *snapshotBackups) Start(ctx context.Context, k8sclient client.Client, cluster *api.PerconaServerMongoDB, cr *api.PerconaServerMongoDBBackup) (api.PerconaServerMongoDBBackupStatus, error) {
	log := logf.FromContext(ctx).WithValues("backup", cr.Name)

	log.Info("Starting snapshot backup")

	var status api.PerconaServerMongoDBBackupStatus

	name := time.Now().UTC().Format(time.RFC3339)
	cmd := ctrl.Cmd{
		Cmd: ctrl.CmdBackup,
		Backup: &ctrl.BackupCmd{
			Name: name,
			Type: defs.ExternalBackup,
		},
	}

	log.Info("Sending backup command", "backupCmd", cmd)

	if err := b.pbm.SendCmd(ctx, cmd); err != nil {
		return status, err
	}
	status = api.PerconaServerMongoDBBackupStatus{
		PBMname: name,
		LastTransition: &metav1.Time{
			Time: time.Unix(time.Now().Unix(), 0),
		},
		State: api.BackupStateRequested,
	}
	if cluster.Spec.Sharding.Enabled && cluster.Spec.Sharding.ConfigsvrReplSet != nil {
		status.ReplsetNames = append(status.ReplsetNames, cluster.Spec.Sharding.ConfigsvrReplSet.Name)
	}
	for _, rs := range cluster.Spec.Replsets {
		status.ReplsetNames = append(status.ReplsetNames, rs.Name)
	}

	return status, nil
}

func (b *snapshotBackups) reconcileSnapshot(
	ctx context.Context,
	cl client.Client,
	rsName string,
	pvc string,
	bcp *api.PerconaServerMongoDBBackup,
) (*volumesnapshotv1.VolumeSnapshot, error) {
	volumeSnapshot := &volumesnapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.VolumeSnapshotName(bcp, rsName),
			Namespace: bcp.GetNamespace(),
		},
	}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(volumeSnapshot), volumeSnapshot); err == nil {
		return volumeSnapshot, nil
	} else if client.IgnoreNotFound(err) != nil {
		return nil, errors.Wrap(err, "get volume snapshot")
	}

	volumeSnapshot.Spec = volumesnapshotv1.VolumeSnapshotSpec{
		VolumeSnapshotClassName: bcp.Spec.VolumeSnapshotClass,
		Source: volumesnapshotv1.VolumeSnapshotSource{
			PersistentVolumeClaimName: &pvc,
		},
	}
	if err := controllerutil.SetControllerReference(bcp, volumeSnapshot, cl.Scheme()); err != nil {
		return nil, errors.Wrap(err, "set controller reference")
	}

	if err := cl.Create(ctx, volumeSnapshot); err != nil {
		return nil, errors.Wrap(err, "create volume snapshot")
	}
	return volumeSnapshot, nil
}

func (b *snapshotBackups) reconcileSnapshots(
	ctx context.Context,
	cl client.Client,
	bcp *api.PerconaServerMongoDBBackup,
	meta *backup.BackupMeta,
) (bool, []api.SnapshotInfo, error) {
	done := true
	snapshots := make([]api.SnapshotInfo, 0)

	podName := func(nodeName string) (string, error) {
		parts := strings.Split(nodeName, ".")
		if parts[0] == "" {
			return "", errors.Errorf("unexpected node name format: %s", nodeName)
		}
		return parts[0], nil
	}

	for _, rs := range meta.Replsets {
		// do not snapshot nodes that are not yet copy ready.
		if rs.Status != defs.StatusCopyReady {
			done = false
			continue
		}

		// parse pod name from node name.
		podName, err := podName(rs.Node)
		if err != nil {
			return false, nil, errors.Wrap(err, "get pod name")
		}

		// ensure snapshot is created.
		pvcName := config.MongodDataVolClaimName + "-" + podName
		snapshot, err := b.reconcileSnapshot(ctx, cl, rs.Name, pvcName, bcp)
		if err != nil {
			return false, nil, errors.Wrap(err, "reconcile snapshot")
		}

		if snapshot.Status == nil || !ptr.Deref(snapshot.Status.ReadyToUse, false) {
			done = false
		}

		// If there is an error, return error.
		// Note that some errors may be transient, but the controller will retry.
		if snapshot.Status != nil && snapshot.Status.Error != nil && ptr.Deref(snapshot.Status.Error.Message, "") != "" {
			return false, nil, errors.Errorf("snapshot error: %s", ptr.Deref(snapshot.Status.Error.Message, ""))
		}
		snapshots = append(snapshots, api.SnapshotInfo{
			ReplsetName:  rs.Name,
			SnapshotName: snapshot.GetName(),
		})
	}
	return done, snapshots, nil
}

func (b *snapshotBackups) Status(ctx context.Context, cl client.Client, cluster *api.PerconaServerMongoDB, cr *api.PerconaServerMongoDBBackup) (api.PerconaServerMongoDBBackupStatus, error) {
	status := cr.Status
	log := logf.FromContext(ctx).WithName("backupStatus").WithValues("backup", cr.Name, "pbmName", status.PBMname)

	meta, err := b.pbm.GetBackupByName(ctx, cr.Status.PBMname)
	if err != nil && !errors.Is(err, pbmErrors.ErrNotFound) {
		return status, errors.Wrap(err, "get pbm backup meta")
	}

	if meta == nil || meta.Name == "" || errors.Is(err, pbmErrors.ErrNotFound) {
		logf.FromContext(ctx).Info("Waiting for backup metadata", "pbmName", cr.Status.PBMname, "backup", cr.Name)
		return status, nil
	}

	log.V(1).Info("Got backup meta", "meta", meta)

	if meta.StartTS > 0 {
		status.StartAt = &metav1.Time{
			Time: time.Unix(meta.StartTS, 0),
		}
	}

	switch meta.Status {
	case defs.StatusError:
		status.State = api.BackupStateError
		status.Error = fmt.Sprintf("%v", meta.Error())

	case defs.StatusStarting:
		passed := time.Now().UTC().Sub(time.Unix(meta.StartTS, 0))
		timeoutSeconds := defaultPBMStartingDeadline
		if s := cluster.Spec.Backup.StartingDeadlineSeconds; s != nil && *s > 0 {
			timeoutSeconds = *s
		}
		if passed >= time.Duration(timeoutSeconds)*time.Second {
			status.State = api.BackupStateError
			status.Error = pbmStartingDeadlineErrMsg
			break
		}

		status.State = api.BackupStateRequested

	case defs.StatusDone:
		status.State = api.BackupStateReady
		status.CompletedAt = &metav1.Time{
			Time: time.Unix(meta.LastTransitionTS, 0),
		}
		status.LastWriteAt = &metav1.Time{
			Time: time.Unix(int64(meta.LastWriteTS.T), 0),
		}

	case defs.StatusCopyReady:
		status.State = api.BackupStateRunning
		snapshotsReady, snapshots, err := b.reconcileSnapshots(ctx, cl, cr, meta)
		if err != nil {
			return status, errors.Wrap(err, "reconcile snapshots")
		}
		status.Snapshots = snapshots
		if snapshotsReady {
			if err := b.pbm.FinishBackup(ctx, cr.Status.PBMname); err != nil {
				return status, errors.Wrap(err, "finish backup")
			}
		}
	}

	status.LastTransition = &metav1.Time{
		Time: time.Unix(meta.LastTransitionTS, 0),
	}
	status.Type = cr.Spec.Type
	return status, nil
}
