package vault

import (
	"context"
	"errors"
	"testing"

	vault "github.com/hashicorp/vault/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func TestNew(t *testing.T) {
	ctx := t.Context()

	t.Run("no address", func(t *testing.T) {
		cr := newCluster("cr", "new")
		cr.Spec.VaultSpec.EndpointURL = ""

		cl := newFakeClient(t)

		v, err := New(ctx, cl, cr)
		require.NoError(t, err)
		assert.Nil(t, v, "expected Vault to be nil when Address is empty")
	})

	t.Run("TLSSecret not found", func(t *testing.T) {
		cr := newCluster("cr", "new")
		cr.Spec.VaultSpec.EndpointURL = "https://vault.example.com"
		cr.Spec.VaultSpec.TLSSecret = "vault-tls"

		cl := newFakeClient(t)

		v, err := New(ctx, cl, cr)
		assert.Nil(t, v, "expected Vault to be nil on error")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "get vault tls secret")
	})

	t.Run("no CA in secret", func(t *testing.T) {
		cr := newCluster("cr", "new")
		cr.Spec.VaultSpec.EndpointURL = "https://vault.example.com"
		cr.Spec.VaultSpec.TLSSecret = "vault-tls"

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "vault-tls",
				Namespace: cr.Namespace,
			},
			Data: map[string][]byte{
				"something-else.crt": []byte("some-data"),
			},
		}

		cl := newFakeClient(t, secret)

		v, err := New(ctx, cl, cr)
		assert.Nil(t, v, "expected Vault to be nil on error")
		require.Error(t, err)

		assert.Equal(t, "tls secret does not have ca.crt key", err.Error())
	})
}

func TestGetUsersSecret(t *testing.T) {
	tests := []struct {
		name         string
		getFn        func(ctx context.Context, p string) (*vault.KVSecret, error)
		expectedData map[string]any
		expectedErr  string
	}{
		{
			name: "secret found",
			getFn: func(ctx context.Context, p string) (*vault.KVSecret, error) {
				return &vault.KVSecret{
					Data: map[string]any{
						"user1": "password1",
						"user2": "password2",
					},
				}, nil
			},
			expectedData: map[string]any{
				"user1": "password1",
				"user2": "password2",
			},
		},
		{
			name: "secret is not found",
			getFn: func(ctx context.Context, p string) (*vault.KVSecret, error) {
				return nil, vault.ErrSecretNotFound
			},
		},
		{
			name: "error",
			getFn: func(ctx context.Context, p string) (*vault.KVSecret, error) {
				return nil, errors.New("test error")
			},
			expectedErr: "unable to read secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fkv := &fakeKV{
				getFn: func(ctx context.Context, p string) (*vault.KVSecret, error) {
					return tt.getFn(ctx, p)
				},
			}
			fc := &fakeClient{kv: fkv}

			v := &Vault{
				c: fc,
			}

			data, err := v.getUsersSecret(t.Context())
			if tt.expectedErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.expectedData, data)
		})
	}
}

func TestFillSecretData(t *testing.T) {
	tests := []struct {
		name           string
		vaultData      map[string]any
		initialData    map[string][]byte
		expectedData   map[string][]byte
		expectedUpdate bool
		expectedErr    string
		nilVault       bool
	}{
		{
			name: "no changes",
			vaultData: map[string]any{
				"user": "password",
			},
			initialData: map[string][]byte{
				"user": []byte("password"),
			},
			expectedData: map[string][]byte{
				"user": []byte("password"),
			},
		},
		{
			name: "no initial data",
			vaultData: map[string]any{
				"user": "password",
			},
			initialData: map[string][]byte{},
			expectedData: map[string][]byte{
				"user": []byte("password"),
			},
			expectedUpdate: true,
		},
		{
			name: "update data from vault",
			vaultData: map[string]any{
				"user": "newpass",
			},
			initialData: map[string][]byte{
				"user": []byte("oldpass"),
			},
			expectedData: map[string][]byte{
				"user": []byte("newpass"),
			},
			expectedUpdate: true,
		},
		{
			name: "invalid data from vault",
			vaultData: map[string]any{
				"user": 123,
			},
			initialData: map[string][]byte{},
			expectedErr: "value type assertion failed",
		},
		{
			name:      "empty vault data",
			vaultData: map[string]any{},
			initialData: map[string][]byte{
				"user": []byte("password"),
			},
			expectedData: map[string][]byte{
				"user": []byte("password"),
			},
			expectedUpdate: false,
		},
		{
			name:     "nil vault",
			nilVault: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fkv := &fakeKV{
				getFn: func(ctx context.Context, p string) (*vault.KVSecret, error) {
					return &vault.KVSecret{Data: tt.vaultData}, nil
				},
			}
			fc := &fakeClient{kv: fkv}

			v := &Vault{
				c: fc,
			}
			if tt.nilVault {
				v = nil
			}

			updated, err := v.FillSecretData(t.Context(), tt.initialData)
			if tt.expectedErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedUpdate, updated)
			assert.Equal(t, tt.expectedData, tt.initialData)
		})
	}
}

func newFakeClient(t *testing.T, objs ...ctrlclient.Object) ctrlclient.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		Build()
}

func newCluster(name, namespace string) *api.PerconaServerMongoDB {
	cr := new(api.PerconaServerMongoDB)
	cr.Name = name
	cr.Namespace = namespace
	cr.Spec.Secrets = new(api.SecretsSpec)
	cr.Spec.VaultSpec.SyncUsersSpec.Role = "my-role"

	return cr
}

type fakeKV struct {
	getFn func(ctx context.Context, p string) (*vault.KVSecret, error)
}

func (f *fakeKV) Get(ctx context.Context, p string) (*vault.KVSecret, error) {
	if f.getFn != nil {
		return f.getFn(ctx, p)
	}
	return nil, nil
}

type fakeClient struct {
	kv kvReader
}

func (f *fakeClient) KVv2(_ string) kvReader {
	return f.kv
}
