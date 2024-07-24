package naming

import (
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

const perconaPrefix = "percona.com/"

const (
	FinalizerDeleteBackup           = perconaPrefix + "delete-backup"
	FinalizerDeletePVC              = perconaPrefix + "delete-psmdb-pvc"
	FinalizerDeletePSMDBPodsInOrder = perconaPrefix + "delete-psmdb-pods-in-order"
)

func GetOrderedFinalizers(cr *api.PerconaServerMongoDB) []string {
	order := []string{FinalizerDeletePSMDBPodsInOrder, FinalizerDeletePVC}

	if cr.CompareVersion("1.17.0") < 0 {
		order = []string{"delete-psmdb-pods-in-order", "delete-psmdb-pvc"}
	}

	finalizers := make([]string, len(cr.GetFinalizers()))
	copy(finalizers, cr.GetFinalizers())
	orderedFinalizers := make([]string, 0, len(finalizers))

	for _, v := range order {
		for i := 0; i < len(finalizers); {
			if v == finalizers[i] {
				orderedFinalizers = append(orderedFinalizers, v)
				finalizers = append(finalizers[:i], finalizers[i+1:]...)
				continue
			}
			i++
		}
	}

	orderedFinalizers = append(orderedFinalizers, finalizers...)
	return orderedFinalizers
}
