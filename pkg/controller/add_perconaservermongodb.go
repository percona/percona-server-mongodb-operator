package controller

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/controller/perconaservermongodb"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, perconaservermongodb.Add)
}
