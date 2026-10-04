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
						"mydb-rs0-2": {"ext": "rs0-0.example.com:27018"},
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

	assert.Equal(t, expected, actual)
}

func TestGetCertificateSansMongosServicePerPod(t *testing.T) {
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
			Replsets: []*api.ReplsetSpec{{Name: "rs0"}},
			Sharding: api.Sharding{
				Enabled: true,
				Mongos: &api.MongosSpec{
					Size: 2,
					Expose: api.MongosExpose{
						ServicePerPod: true,
					},
				},
			},
		},
	}

	actual := GetCertificateSans(cr)

	for _, san := range []string{
		"mydb-mongos-0",
		"mydb-mongos-0.myns",
		"mydb-mongos-0.myns.cluster.service.dns.suffix",
		"mydb-mongos-0.myns.clusters.example",
		"mydb-mongos-1",
		"mydb-mongos-1.myns",
		"mydb-mongos-1.myns.cluster.service.dns.suffix",
		"mydb-mongos-1.myns.clusters.example",
	} {
		assert.Contains(t, actual, san)
	}

	assert.NotContains(t, actual, "mydb-mongos-2")

	cr.Spec.Sharding.Mongos.Expose.ServicePerPod = false
	assert.NotContains(t, GetCertificateSans(cr), "mydb-mongos-0")
}

func TestSansToIssue(t *testing.T) {
	tests := map[string]struct {
		current []string
		desired []string
		sans    []string
		reissue bool
	}{
		"equal": {
			current: []string{"a", "b"},
			desired: []string{"a", "b"},
			sans:    []string{"a", "b"},
		},
		"current has extra sans": {
			current: []string{"a", "b", "c"},
			desired: []string{"a", "b"},
			sans:    []string{"a", "b", "c"},
		},
		"desired san is missing": {
			current: []string{"a", "b"},
			desired: []string{"a", "b", "c"},
			sans:    []string{"a", "b", "c"},
			reissue: true,
		},
		"replaced san": {
			current: []string{"a", "b"},
			desired: []string{"a", "c"},
			sans:    []string{"a", "c"},
			reissue: true,
		},
		"empty cert": {
			current: nil,
			desired: []string{"a"},
			sans:    []string{"a"},
			reissue: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			sans, reissue := SansToIssue(tt.current, tt.desired)
			assert.Equal(t, tt.sans, sans)
			assert.Equal(t, tt.reissue, reissue)
		})
	}
}
