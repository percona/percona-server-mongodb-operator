package naming

import (
	"k8s.io/utils/ptr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func AppProtocol(cr *api.PerconaServerMongoDB) *string {
	if cr.CompareVersion("1.22.0") >= 0 {
		// Kubernetes recommends using IANA-registered service names for `appProtocol`.
		// `mongodb` is an IANA-registered service name:
		// https://www.iana.org/assignments/service-names-port-numbers/service-names-port-numbers.xhtml?search=27017
		//
		// However, Istio expects `appProtocol: mongo`, so we use that instead.
		// https://istio.io/latest/docs/ops/configuration/traffic-management/protocol-selection/#explicit-protocol-selection
		return ptr.To("mongo")
	}
	return nil
}
