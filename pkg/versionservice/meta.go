package versionservice

import (
	"context"
	"strings"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/percona/percona-backup-mongodb/pbm/defs"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func BuildMeta(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, operatorDepl *appsv1.Deployment, kubeVersion, fcv string) (Meta, error) {
	watchNs, err := k8s.GetWatchNamespace()
	if err != nil {
		return Meta{}, errors.Wrap(err, "get WATCH_NAMESPACE env variable")
	}
	vm := Meta{
		Apply:                  string(cr.Spec.UpgradeOptions.Apply),
		CRUID:                  string(cr.GetUID()),
		Version:                cr.Version().String(),
		PMMEnabled:             cr.Spec.PMM.Enabled,
		PMMVersion:             cr.Status.PMMVersion,
		MCSEnabled:             cr.Spec.MultiCluster.Enabled,
		KubeVersion:            kubeVersion,
		PITREnabled:            cr.Spec.Backup.PITR.Enabled,
		ClusterSize:            cr.Status.Size,
		MongoVersion:           cr.Status.MongoVersion,
		BackupVersion:          cr.Status.BackupVersion,
		BackupsEnabled:         cr.Spec.Backup.Enabled && len(cr.Spec.Backup.Storages) > 0,
		ShardingEnabled:        cr.Spec.Sharding.Enabled,
		ClusterWideEnabled:     len(watchNs) == 0 || len(strings.Split(watchNs, ",")) > 1,
		HashicorpVaultEnabled:  len(cr.Spec.Secrets.Vault) > 0,
		RoleManagementEnabled:  len(cr.Spec.Roles) > 0,
		UserManagementEnabled:  len(cr.Spec.Users) > 0,
		VolumeExpansionEnabled: cr.Spec.IsVolumeExpansionEnabled(),
		VectorSearchEnabled:    cr.IsSearchEnabled(),
		TLSMode:                string(cr.Spec.TLS.Mode),
	}

	if cr.Spec.Sharding.Enabled && cr.Spec.Sharding.Mongos != nil {
		vm.MongosSize = cr.Spec.Sharding.Mongos.Size
	}

	if cr.Spec.Platform != nil {
		vm.Platform = string(*cr.Spec.Platform)
	}

	for _, rs := range cr.Spec.Replsets {
		if len(rs.Sidecars) > 0 {
			vm.SidecarsUsed = true
		}
		if rs.Arbiter.Enabled {
			vm.ArbiterEnabled = true
		}
		if rs.NonVoting.Enabled {
			vm.NonVotingEnabled = true
		}
		encryptionEnabled, err := rs.IsEncryptionEnabled()
		if err != nil {
			return Meta{}, errors.Wrapf(err, "check if encryption is enabled for replset %s", rs.Name)
		}
		if encryptionEnabled {
			vm.EncryptionEnabled = true
		}
	}

	for _, stg := range cr.Spec.Backup.Storages {
		switch stg.Type {
		case api.BackupStorageOCI:
			vm.OCIBackupEnabled = true
		case api.BackupStorageOSS:
			vm.AlibabaBackupEnabled = true
		case api.BackupStorageS3:
			if strings.Contains(stg.S3.EndpointURL, naming.OSSCloudEndpointURL) {
				vm.AlibabaBackupEnabled = true
			}
		}
	}

	clusterSyncList := new(api.PerconaServerMongoDBClusterSyncList)
	if err := cl.List(ctx, clusterSyncList, &client.ListOptions{Namespace: cr.Namespace}); err != nil {
		return Meta{}, errors.Wrap(err, "list PerconaServerMongoDBClusterSync")
	}
	vm.ClusterSyncEnabled = len(clusterSyncList.Items) > 0

	if _, ok := operatorDepl.Labels["helm.sh/chart"]; ok {
		vm.HelmDeployOperator = true
	}

	if _, ok := cr.Labels["helm.sh/chart"]; ok {
		vm.HelmDeployCR = true
	}

	for _, task := range cr.Spec.Backup.Tasks {
		if task.Type == defs.PhysicalBackup && task.Enabled {
			vm.PhysicalBackupScheduled = true
			break
		}
	}

	req, err := majorUpgradeRequested(cr, fcv)
	if err != nil {
		return Meta{}, errors.Wrap(err, "check if major update requested")
	}
	if req.Ok {
		if len(req.Apply) != 0 {
			vm.Apply = req.Apply
			vm.MongoVersion = req.NewVersion
		} else {
			vm.Apply = req.NewVersion
		}
	}

	return vm, nil
}
