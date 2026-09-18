package tls

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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

func TestConfig(t *testing.T) {
	tests := map[string]struct {
		dnsMode            api.DNSMode
		partialCert        bool
		insecureSkipVerify bool
	}{
		"internal": {
			dnsMode:            api.DNSModeInternal,
			insecureSkipVerify: false,
		},
		"external": {
			dnsMode:            api.DNSModeExternal,
			insecureSkipVerify: true,
		},
		"internal with certificate missing sans": {
			dnsMode:            api.DNSModeInternal,
			partialCert:        true,
			insecureSkipVerify: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mydb",
					Namespace: "myns",
				},
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion:             version.Version(),
					ClusterServiceDNSMode: tt.dnsMode,
					Secrets:               &api.SecretsSpec{SSL: "mydb-ssl"},
					Replsets:              []*api.ReplsetSpec{{Name: "rs0"}},
				},
			}
			sans := GetCertificateSans(cr)
			if tt.partialCert {
				sans = []string{"localhost"}
			}
			caCert, tlsCert, tlsKey, err := Issue(sans)
			require.NoError(t, err)

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mydb-ssl",
					Namespace: "myns",
				},
				Data: map[string][]byte{
					"ca.crt":  caCert,
					"tls.crt": tlsCert,
					"tls.key": tlsKey,
				},
			}
			cl := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(secret).Build()

			cfg, err := Config(t.Context(), cl, cr)
			require.NoError(t, err)
			assert.Equal(t, tt.insecureSkipVerify, cfg.InsecureSkipVerify)
			assert.NotNil(t, cfg.RootCAs)
			assert.Len(t, cfg.Certificates, 1)
		})
	}
}

func TestInsecureSkipVerify(t *testing.T) {
	newCR := func(dnsMode api.DNSMode) *api.PerconaServerMongoDB {
		return &api.PerconaServerMongoDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "mydb",
				Namespace: "myns",
			},
			Spec: api.PerconaServerMongoDBSpec{
				CRVersion:             version.Version(),
				ClusterServiceDNSMode: dnsMode,
				Replsets:              []*api.ReplsetSpec{{Name: "rs0"}},
			},
		}
	}

	issueFor := func(t *testing.T, sans []string) []byte {
		t.Helper()
		_, tlsCert, _, err := Issue(sans)
		require.NoError(t, err)
		return tlsCert
	}

	t.Run("certificate covers all sans", func(t *testing.T) {
		cr := newCR(api.DNSModeInternal)
		assert.False(t, InsecureSkipVerify(cr, issueFor(t, GetCertificateSans(cr))))
	})

	t.Run("certificate has extra sans", func(t *testing.T) {
		cr := newCR(api.DNSModeInternal)
		assert.False(t, InsecureSkipVerify(cr, issueFor(t, append(GetCertificateSans(cr), "extra.example.com"))))
	})

	t.Run("certificate is missing a san", func(t *testing.T) {
		cr := newCR(api.DNSModeInternal)
		assert.True(t, InsecureSkipVerify(cr, issueFor(t, []string{"localhost"})))
	})

	t.Run("certificate is unparseable", func(t *testing.T) {
		cr := newCR(api.DNSModeInternal)
		assert.True(t, InsecureSkipVerify(cr, []byte("not-a-cert")))
	})

	t.Run("cluster dials addresses outside the sans", func(t *testing.T) {
		cr := newCR(api.DNSModeExternal)
		assert.True(t, InsecureSkipVerify(cr, issueFor(t, GetCertificateSans(cr))))
	})
}
