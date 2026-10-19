package apis

import (
	"k8s.io/apimachinery/pkg/runtime"

	v1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

// AddToSchemes may be used to add all resources defined in the project to a Scheme
var AddToSchemes = runtime.SchemeBuilder{
	v1.AddToScheme,
}

// AddToScheme adds all Resources to the Scheme
func AddToScheme(s *runtime.Scheme) error {
	return AddToSchemes.AddToScheme(s)
}
