package backup

import (
	"fmt"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
)

const (
	DefaultVersion = "0.1.0"

	backupImagePrefix = "percona/percona-server-mongodb-operator"
)

type Controller struct {
	client        sdk.Client
	psmdb         *v1alpha1.PerconaServerMongoDB
	serverVersion *v1alpha1.ServerVersion
	usersSecret   *corev1.Secret
}

func New(client sdk.Client, psmdb *v1alpha1.PerconaServerMongoDB, serverVersion *v1alpha1.ServerVersion, usersSecret *corev1.Secret) *Controller {
	return &Controller{
		client:        client,
		psmdb:         psmdb,
		serverVersion: serverVersion,
		usersSecret:   usersSecret,
	}
}

func (c *Controller) getImageName(component string) string {
	if c.psmdb.Spec.Backup.Version != "" {
		return fmt.Sprintf("%s:%s-backup-%s", backupImagePrefix, c.psmdb.Spec.Backup.Version, component)
	}
	return fmt.Sprintf("%s:backup-%s", backupImagePrefix, component)
}
