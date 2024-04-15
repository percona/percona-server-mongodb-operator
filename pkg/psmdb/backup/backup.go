package backup

import (
	"context"
	"strings"

	"github.com/pkg/errors"
	client "sigs.k8s.io/controller-runtime/pkg/client"
	log "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

type JobType int

const (
	TypeBackup JobType = iota
	TypeRestore
	TypePITRestore
)

type Job struct {
	Name string
	Type JobType
}

func NewBackupJob(name string) Job {
	return Job{
		Name: name,
		Type: TypeBackup,
	}
}

func NewRestoreJob(cr *api.PerconaServerMongoDBRestore) Job {
	j := Job{
		Name: cr.Name,
		Type: TypeRestore,
	}

	if cr.Spec.PITR != nil {
		j.Type = TypePITRestore
	}

	return j
}

func IsPBMNotConfiguredError(err error) bool {
	return strings.Contains(err.Error(), "mongo: no documents in result")
}

// HasActiveJobs returns true if there are running backups or restores
// in given cluster and namespace
func HasActiveJobs(ctx context.Context, newPBMFunc NewPBMFunc, cl client.Client, cluster *api.PerconaServerMongoDB, current Job, allowLock ...LockHeaderPredicate) (bool, error) {
	l := log.FromContext(ctx)
	l.V(1).Info("Checking for active jobs", "currentJob", current)

	bcps := &api.PerconaServerMongoDBBackupList{}
	err := cl.List(ctx,
		bcps,
		&client.ListOptions{
			Namespace: cluster.Namespace,
		},
	)
	if err != nil {
		return false, errors.Wrap(err, "get backup list")
	}
	for _, b := range bcps.Items {
		if b.Name == current.Name && current.Type == TypeBackup {
			continue
		}
		if b.Spec.GetClusterName() == cluster.Name &&
			b.Status.State != api.BackupStateReady &&
			b.Status.State != api.BackupStateError &&
			b.Status.State != api.BackupStateWaiting {
			l.Info("Waiting for backup to complete", "backup", b.Name, "status", b.Status.State)
			return true, nil
		}
	}

	rstrs := &api.PerconaServerMongoDBRestoreList{}
	err = cl.List(ctx,
		rstrs,
		&client.ListOptions{
			Namespace: cluster.Namespace,
		},
	)
	if err != nil {
		return false, errors.Wrap(err, "get restore list")
	}
	for _, r := range rstrs.Items {
		if r.Name == current.Name && (current.Type == TypeRestore || current.Type == TypePITRestore) {
			continue
		}
		if r.Spec.ClusterName == cluster.Name &&
			r.Status.State != api.RestoreStateReady &&
			r.Status.State != api.RestoreStateError &&
			r.Status.State != api.RestoreStateWaiting {
			l.Info("Waiting for restore to complete", "restore", r.Name, "status", r.Status.State)
			return true, nil
		}
	}

	pbm, err := newPBMFunc(ctx, cl, cluster)
	if err != nil {
		if IsPBMNotConfiguredError(err) {
			return false, nil
		}
		return false, errors.Wrap(err, "getting PBM object")
	}
	defer pbm.Close(ctx)

	allowLock = append(allowLock, NotJobLock(current))
	hasLocks, err := pbm.HasLocks(ctx, allowLock...)
	if err != nil {
		return false, errors.Wrap(err, "check PBM locks")
	}

	if hasLocks {
		l.Info("Waiting for PBM locks to be relased")
	}

	return hasLocks, nil
}
