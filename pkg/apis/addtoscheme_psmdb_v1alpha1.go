package apis

import (
	"github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func init() {
	// Register the types with the Scheme so the components can map objects to GroupVersionKinds and back
	AddToSchemes = append(AddToSchemes, v1.SchemeBuilder.AddToScheme)
}
