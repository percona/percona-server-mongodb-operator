package naming

import (
	"strings"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func SecretDatabaseAdminConnStrName(cr *api.PerconaServerMongoDB) string {
	return strings.ToLower(cr.Name + "-" + string(api.RoleDatabaseAdmin) + "-conn-str")
}

func SecretCustomUserConnStrName(cr *api.PerconaServerMongoDB, user *api.User) string {
	return user.SecretName(cr) + "-conn-str"
}
