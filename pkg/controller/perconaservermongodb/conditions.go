package perconaservermongodb

import (
	"context"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func (r *ReconcilePerconaServerMongoDB) updateCondition(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB, c psmdbv1.ClusterCondition) error {
	cr.Status.AddCondition(c)
	return r.writeStatus(ctx, cr)
}
