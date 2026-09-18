package tls

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestGetCertificateSans(t *testing.T) {
	cr := &api.PerconaServerMongoDB{
		Name:      "mydb",
		Namespace: "myns",
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

func TestGetCertificateSansHorizonOverrides(t *testing.T) {
	newCR := func(crVersion string, rs *api.ReplsetSpec) *api.PerconaServerMongoDB {
		return &api.PerconaServerMongoDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "mydb",
				Namespace: "myns",
			},
			Spec: api.PerconaServerMongoDBSpec{
				CRVersion:               crVersion,
				ClusterServiceDNSSuffix: "svc.cluster.local",
				MultiCluster:            api.MultiCluster{DNSSuffix: "svc.clusterset.local"},
				Replsets:                []*api.ReplsetSpec{rs},
			},
		}
	}

	overridesOnly := func() *api.ReplsetSpec {
		return &api.ReplsetSpec{
			Name: "rs0",
			ReplsetOverrides: api.ReplsetOverrides{
				"mydb-rs0-0": {Horizons: map[string]string{"ext": "rs0-0.example.com:27017"}},
				"mydb-rs0-1": {Horizons: map[string]string{"ext": "rs0-1.example.com"}},
			},
		}
	}

	tests := map[string]struct {
		crVersion string
		replset   *api.ReplsetSpec
		expected  []string
	}{
		"overrides only": {
			crVersion: version.Version(),
			replset:   overridesOnly(),
			expected:  []string{"rs0-0.example.com", "rs0-1.example.com"},
		},
		"overrides only on older crVersion": {
			crVersion: "1.23.0",
			replset:   overridesOnly(),
			expected:  nil,
		},
		"override wins over splitHorizons": {
			crVersion: version.Version(),
			replset: &api.ReplsetSpec{
				Name: "rs0",
				Horizons: api.HorizonsSpec{
					"mydb-rs0-0": {"ext": "rs0-0.example.com"},
					"mydb-rs0-1": {"ext": "rs0-1.example.com"},
				},
				ReplsetOverrides: api.ReplsetOverrides{
					"mydb-rs0-0": {Horizons: map[string]string{"ext": "override.example.com"}},
					"mydb-rs0-2": {Horizons: map[string]string{"ext": "rs0-1.example.com"}},
				},
			},
			expected: []string{"override.example.com", "rs0-1.example.com"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			withHorizons := GetCertificateSans(newCR(tt.crVersion, tt.replset))
			base := GetCertificateSans(newCR(tt.crVersion, &api.ReplsetSpec{Name: "rs0"}))

			var horizonSans []string
			for _, san := range withHorizons {
				if !slices.Contains(base, san) {
					horizonSans = append(horizonSans, san)
				}
			}
			assert.Equal(t, tt.expected, horizonSans)
			assert.Len(t, withHorizons, len(base)+len(tt.expected))
		})
	}
}
