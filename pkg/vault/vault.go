package vault

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"net/url"
	"path"
	"time"

	vault "github.com/hashicorp/vault/api"
	auth "github.com/hashicorp/vault/api/auth/kubernetes"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

type kvClient interface {
	KVv2(mountPath string) kvReader
}

type kvReader interface {
	Get(ctx context.Context, path string) (*vault.KVSecret, error)
}

type vaultClient struct {
	c *vault.Client
}

func (r *vaultClient) KVv2(mountPath string) kvReader {
	return &vaultReader{kv: r.c.KVv2(mountPath)}
}

type vaultReader struct {
	kv *vault.KVv2
}

func (r *vaultReader) Get(ctx context.Context, path string) (*vault.KVSecret, error) {
	return r.kv.Get(ctx, path)
}

type Vault struct {
	c kvClient

	mountPath string
	keyPath   string
}

type CachedVault struct {
	hash [16]byte

	lastUpdatedAt  time.Time
	reinitInterval time.Duration

	*Vault
}

func (cv *CachedVault) Update(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) error {
	if cv == nil || cr.Spec.VaultSpec.EndpointURL == "" {
		return nil
	}

	if cv.reinitInterval == 0 {
		cv.reinitInterval = 30 * time.Minute
	}

	changed, err := cv.updateHash(cr)
	if err != nil {
		return errors.Wrap(err, "update hash")
	}
	if !changed && time.Since(cv.lastUpdatedAt) <= cv.reinitInterval {
		return nil
	}

	cv.Vault, err = New(ctx, cl, cr)
	if err != nil {
		return errors.Wrap(err, "new vault")
	}

	cv.lastUpdatedAt = time.Now()

	return nil
}

func (cv *CachedVault) updateHash(cr *api.PerconaServerMongoDB) (bool, error) {
	spec := cr.Spec.VaultSpec
	data, err := json.Marshal(spec)
	if err != nil {
		return false, err
	}

	newHash := md5.Sum(data)
	changed := !bytes.Equal(newHash[:], cv.hash[:])
	cv.hash = newHash
	return changed, nil
}

func New(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) (*Vault, error) {
	spec := cr.Spec.VaultSpec
	if spec.EndpointURL == "" {
		return nil, nil
	}
	_, err := url.Parse(spec.EndpointURL)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse endpointURL")
	}

	config := vault.DefaultConfig()
	config.Address = spec.EndpointURL

	if spec.TLSSecret != "" {
		secret := new(corev1.Secret)
		if err := cl.Get(ctx, types.NamespacedName{
			Name:      spec.TLSSecret,
			Namespace: cr.Namespace,
		}, secret); err != nil {
			return nil, errors.Wrap(err, "get vault tls secret")
		}

		ca, ok := secret.Data["ca.crt"]
		if !ok {
			return nil, errors.New("tls secret does not have ca.crt key")
		}

		if err := config.ConfigureTLS(&vault.TLSConfig{
			CACertBytes: ca,
		}); err != nil {
			return nil, errors.Wrap(err, "configure TLS")
		}
	}

	client, err := vault.NewClient(config)
	if err != nil {
		return nil, errors.Wrap(err, "unable to initialize Vault client")
	}

	if spec.SyncUsersSpec.TokenSecret != "" {
		tokenSecret := new(corev1.Secret)
		if err := cl.Get(ctx, types.NamespacedName{Name: spec.SyncUsersSpec.TokenSecret, Namespace: cr.Namespace}, tokenSecret); err != nil {
			return nil, errors.Wrap(err, "failed to get tokenSecret")
		}
		if _, ok := tokenSecret.Data["token"]; !ok {
			return nil, errors.New("expected `token` key is not present in the .syncUsers.tokenSecret data")
		}
		client.SetToken(string(tokenSecret.Data["token"]))
	} else {
		var opts []auth.LoginOption
		k8sAuth, err := auth.NewKubernetesAuth(spec.SyncUsersSpec.Role, opts...)
		if err != nil {
			return nil, errors.Wrap(err, "unable to initialize Kubernetes auth method")
		}

		authSecret, err := client.Auth().Login(ctx, k8sAuth)
		if err != nil {
			return nil, errors.Wrap(err, "unable to log in with Kubernetes auth")
		}
		if authSecret == nil {
			return nil, errors.New("no auth secret was returned after login")
		}
	}

	mountPath := "secret"
	keyPath := path.Join("psmdb", spec.SyncUsersSpec.Role, cr.Namespace, cr.Name, "users")
	if spec.SyncUsersSpec.MountPath != "" {
		mountPath = spec.SyncUsersSpec.MountPath
	}
	if spec.SyncUsersSpec.KeyPath != "" {
		keyPath = spec.SyncUsersSpec.KeyPath
	}
	return &Vault{
		keyPath:   keyPath,
		mountPath: mountPath,
		c:         &vaultClient{c: client},
	}, nil
}

func (v *Vault) FillSecretData(ctx context.Context, data map[string][]byte) (bool, error) {
	if v == nil {
		return false, nil
	}

	vaultData, err := v.getUsersSecret(ctx)
	if err != nil {
		return false, errors.Wrap(err, "get users secret")
	}

	shouldUpdate := false
	for k, v := range vaultData {
		value, ok := v.(string)
		if !ok {
			return false, errors.Errorf("value type assertion failed: %T %#v", v, v)
		}

		secretPass, ok := data[k]
		if !ok || !bytes.Equal(secretPass, []byte(value)) {
			shouldUpdate = true
			data[k] = []byte(value)
		}
	}
	return shouldUpdate, nil
}

func (v *Vault) getUsersSecret(ctx context.Context) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}

	secret, err := v.c.KVv2(v.mountPath).Get(ctx, v.keyPath)
	if errors.Is(err, vault.ErrSecretNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, errors.Wrap(err, "unable to read secret")
	}
	return secret.Data, nil
}
