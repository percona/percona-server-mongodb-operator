package controller

import (
	"github.com/percona/percona-server-mongodb-operator/pkg/controller/perconaservermongodbclustersync"
)

func init() {
	AddToManagerFuncs = append(AddToManagerFuncs, perconaservermongodbclustersync.Add)
}
