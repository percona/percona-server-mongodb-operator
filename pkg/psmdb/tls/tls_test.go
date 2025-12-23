package tls

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestGetCertificateSans(t *testing.T) {
	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mydb",
			Namespace: "myns",
		},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion:               version.Version(),
			ClusterServiceDNSSuffix: "cluster.service.dns.suffix",
			MultiCluster: api.MultiCluster{
				DNSSuffix: "clusters.example",
			},
			Replsets: []*api.ReplsetSpec{
				{
					Name: "rs0",
					Horizons: map[string]map[string]string{
						"mydb-rs0-0": {"ext": "rs0-0.example.com:27017"},
						"mydb-rs0-1": {"ext": "rs0-1.example.com"},
					},
				},
				{
					Name: "rs1",
				},
			},
		},
	}

	actual := GetCertificateSans(cr)

	expected := []string{
		"localhost",

		"mydb-rs0",
		"mydb-rs0.myns",
		"mydb-rs0.myns.cluster.service.dns.suffix",
		"*.mydb-rs0",
		"*.mydb-rs0.myns",
		"*.mydb-rs0.myns.cluster.service.dns.suffix",
		"mydb-rs0.myns.clusters.example",
		"*.mydb-rs0.myns.clusters.example",

		"rs0-0.example.com",
		"rs0-1.example.com",

		"mydb-rs1",
		"mydb-rs1.myns",
		"mydb-rs1.myns.cluster.service.dns.suffix",
		"*.mydb-rs1",
		"*.mydb-rs1.myns",
		"*.mydb-rs1.myns.cluster.service.dns.suffix",
		"mydb-rs1.myns.clusters.example",
		"*.mydb-rs1.myns.clusters.example",

		"*.myns.clusters.example",

		"mydb-mongos",
		"mydb-mongos.myns",
		"mydb-mongos.myns.cluster.service.dns.suffix",
		"*.mydb-mongos",
		"*.mydb-mongos.myns",
		"*.mydb-mongos.myns.cluster.service.dns.suffix",
		"mydb-" + api.ConfigReplSetName,
		"mydb-" + api.ConfigReplSetName + ".myns",
		"mydb-" + api.ConfigReplSetName + ".myns.cluster.service.dns.suffix",
		"*.mydb-" + api.ConfigReplSetName,
		"*.mydb-" + api.ConfigReplSetName + ".myns",
		"*.mydb-" + api.ConfigReplSetName + ".myns.cluster.service.dns.suffix",
		"mydb-mongos.myns.clusters.example",
		"*.mydb-mongos.myns.clusters.example",
		"mydb-" + api.ConfigReplSetName + ".myns.clusters.example",
		"*.mydb-" + api.ConfigReplSetName + ".myns.clusters.example",
	}

	assert.ElementsMatch(t, expected, actual)
}
