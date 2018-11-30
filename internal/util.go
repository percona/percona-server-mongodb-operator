package internal

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
)

// GetContainerRunUID returns an int64-pointer reflecting the user ID a container
// should run as
func GetContainerRunUID(m *v1alpha1.PerconaServerMongoDB, serverVersion *v1alpha1.ServerVersion) *int64 {
	if GetPlatform(m, serverVersion) != v1alpha1.PlatformOpenshift {
		return &m.Spec.RunUID
	}
	return nil
}
