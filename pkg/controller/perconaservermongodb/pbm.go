package perconaservermongodb

import (
	"context"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func (r *ReconcilePerconaServerMongoDB) updatePBMConfig(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB) error {
	if !cr.Spec.Backup.Enabled {
		return nil
	}

	return nil
}
