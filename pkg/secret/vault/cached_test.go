package vault

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func newVaultReadyCluster(name, namespace string) *api.PerconaServerMongoDB {
	cr := newCluster(name, namespace)
	cr.Spec.VaultSpec.EndpointURL = "https://vault.example.com"
	cr.Spec.VaultSpec.TLSSecret = "vault-tls"
	cr.Spec.VaultSpec.SyncUsersSpec.TokenSecret = "vault-token"
	return cr
}

const fakeCACert = `-----BEGIN CERTIFICATE-----
MIIDCzCCAfOgAwIBAgIUebt4OVeDwBNqXWHfIsFb8fl1YqowDQYJKoZIhvcNAQEL
BQAwFTETMBEGA1UEAwwKdmF1bHQtdGVzdDAeFw0yNjA5MjQxMDM2MzZaFw0zNjA5
MjExMDM2MzZaMBUxEzARBgNVBAMMCnZhdWx0LXRlc3QwggEiMA0GCSqGSIb3DQEB
AQUAA4IBDwAwggEKAoIBAQC/yi5NVS8cJ6HCUIIUUvEgXapzdR8384Yen11LHLTo
zRrkTvhTMXTS1ffhZMUaCzOXXyjXerxq77yka/1PNRDDDUC8ekGISdY+1Xmuop3o
BAkelMAppm42K/+4lMc5J3IpGGnQGdZ8e/AeQYjKiacckP26X8qrf2a0dye7N1hl
1Upyo5ym54Kweisvp64AeJML40M6+GVpAmg807Dlx9HZAoUGEbPv1f+FBo7bLxpr
UZsXSBBic7N8j5omJiW9MtdYb5j+ppXt+A1PrcWqF3p1k/czYkUGQOSkaWpcEneC
+NxFFsiTyR3q5TRgqTok2WofbmwOuDAYTcjAs2GZiiKbAgMBAAGjUzBRMB0GA1Ud
DgQWBBRBuah5BqNGfAKJLxTNPyO2DkVRzzAfBgNVHSMEGDAWgBRBuah5BqNGfAKJ
LxTNPyO2DkVRzzAPBgNVHRMBAf8EBTADAQH/MA0GCSqGSIb3DQEBCwUAA4IBAQCt
s+/m9uOoOJD8sJ2340g3sHmIWvVpmgbjBOmOKGI2ilgc8ZCDLEH6YmuvUPdQbbgN
hGNXUxGUeEuFQm+jJhRP3iwoMSOzn5ShqerPmO1kWYlqiaDROJiF+5+Q/KE8/Xi0
tFfk/wftWvNPYwh+TPxsU/hJKmehPBsj34eKgf3Yn2U99eNn1RYofslf4uoQFWRg
UzjRY0NUdK86uoeDVUxx9WTbSyucKNPrqfakFVXvWZ9Qvor3VoTiHjMAhnFNEe4j
kv27q6FkutIXzXAcJbCnlldhh9zaakzKPXose4YJOUtfYD37YNiRUHRENZL1uGhT
Ekc4if1pC1UuXHjFwOqY
-----END CERTIFICATE-----`

func TestCachedClientUpdate_ReinitInterval(t *testing.T) {
	tlsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "vault-tls", Namespace: "new"},
		Data:       map[string][]byte{"ca.crt": []byte(fakeCACert)},
	}
	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "vault-token", Namespace: "new"},
		Data:       map[string][]byte{"token": []byte("fake-token")},
	}
	cl := newFakeClient(t, tlsSecret, tokenSecret)

	t.Run("custom interval is respected", func(t *testing.T) {
		cv := &cachedClient{}
		cr := newVaultReadyCluster("cr", "new")
		cr.Spec.VaultSpec.ReinitInterval = &metav1.Duration{Duration: time.Hour}

		require.NoError(t, cv.Update(t.Context(), cl, cr))
		firstUpdatedAt := cv.lastUpdatedAt
		require.False(t, firstUpdatedAt.IsZero())

		require.NoError(t, cv.Update(t.Context(), cl, cr))
		assert.Equal(t, firstUpdatedAt, cv.lastUpdatedAt, "should not reinit before the custom interval elapses")
	})

	t.Run("default interval is used when not set", func(t *testing.T) {
		cv := &cachedClient{}
		cr := newVaultReadyCluster("cr", "new")
		cr.Spec.VaultSpec.ReinitInterval = nil

		require.NoError(t, cv.Update(t.Context(), cl, cr))
		require.False(t, cv.lastUpdatedAt.IsZero())
	})

	t.Run("changed spec forces reinit regardless of interval", func(t *testing.T) {
		cv := &cachedClient{}
		cr := newVaultReadyCluster("cr", "new")
		cr.Spec.VaultSpec.ReinitInterval = &metav1.Duration{Duration: time.Hour}

		require.NoError(t, cv.Update(t.Context(), cl, cr))
		firstUpdatedAt := cv.lastUpdatedAt

		cr.Spec.VaultSpec.SyncUsersSpec.MountPath = "changed-path"
		require.NoError(t, cv.Update(t.Context(), cl, cr))
		assert.True(t, cv.lastUpdatedAt.After(firstUpdatedAt) || cv.lastUpdatedAt.Equal(firstUpdatedAt))
	})

	t.Run("nil spec is a no-op", func(t *testing.T) {
		cv := &cachedClient{}
		cr := new(api.PerconaServerMongoDB)
		require.NoError(t, cv.Update(t.Context(), cl, cr))
	})
}
