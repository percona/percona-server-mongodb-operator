package controller

import (
	"github.com/percona/percona-server-mongodb-operator/pkg/controller/perconaservermongodbrestore"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, perconaservermongodbrestore.Add)
}
