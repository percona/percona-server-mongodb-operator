package backup

import (
	"context"

	"github.com/pkg/errors"
	client "sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

type JobType int

const (
	TypeBackup JobType = iota
	TypeRestore
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

func NewRestoreJob(name string) Job {
	return Job{
		Name: name,
		Type: TypeRestore,
	}
}

// HasActiveJobs returns true if there are running backups or restores
// in given cluster and namestpace
func HasActiveJobs(cl client.Client, cluster *api.PerconaServerMongoDB, current Job, allowLock ...LockHeaderPredicate) (bool, error) {
	bcps := &api.PerconaServerMongoDBBackupList{}
	err := cl.List(context.TODO(),
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
		if b.Spec.PSMDBCluster == cluster.Name &&
			b.Status.State != api.BackupStateReady &&
			b.Status.State != api.BackupStateError &&
			b.Status.State != api.BackupStateWaiting {
			return true, nil
		}
	}

	rstrs := &api.PerconaServerMongoDBRestoreList{}
	err = cl.List(context.TODO(),
		rstrs,
		&client.ListOptions{
			Namespace: cluster.Namespace,
		},
	)
	if err != nil {
		return false, errors.Wrap(err, "get restore list")
	}
	for _, r := range rstrs.Items {
		if r.Name == current.Name && current.Type == TypeRestore {
			continue
		}
		if r.Spec.ClusterName == cluster.Name &&
			r.Status.State != api.RestoreStateReady &&
			r.Status.State != api.RestoreStateError &&
			r.Status.State != api.RestoreStateWaiting {
			return true, nil
		}
	}

	pbm, err := NewPBM(cl, cluster)
	if err != nil {
		return false, errors.Wrap(err, "getting pbm object")
	}

	allowLock = append([]LockHeaderPredicate{NotJobLock(current)}, allowLock...)

	hasLocks, err := pbm.HasLocks(allowLock...)
	if err != nil {
		return false, errors.Wrap(err, "getting pbm locks")
	}

	if hasLocks {
		return true, nil
	}

	return false, nil
}
