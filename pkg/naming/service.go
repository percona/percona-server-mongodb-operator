package naming

import (
	"k8s.io/utils/ptr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func AppProtocol(cr *api.PerconaServerMongoDB) *string {
	if cr.CompareVersion("1.22.0") >= 0 {
		// `mongodb` is an IANA-registered service name:
		// https://www.iana.org/assignments/service-names-port-numbers/service-names-port-numbers.xhtml?search=27017
		// Kubernetes recommends using IANA-registered service names for `appProtocol`
		return ptr.To("mongodb")
	}
	return nil
}
