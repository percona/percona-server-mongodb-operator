package vault

import (
	"bytes"
	"context"
	"fmt"
	"path"

	vault "github.com/hashicorp/vault/api"
	auth "github.com/hashicorp/vault/api/auth/kubernetes"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

type Vault struct {
	c *vault.Client

	cr *api.PerconaServerMongoDB
}

func New(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) (*Vault, error) {
	spec := cr.Spec.Secrets.VaultSpec
	if spec.Address == "" {
		return nil, nil
	}

	config := vault.DefaultConfig()
	config.Address = spec.Address

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
			return nil, errors.New("tls secret doesn't have ca.crt key")
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

	var opts []auth.LoginOption
	if spec.ServiceAccountTokenPath != "" {
		opts = append(opts, auth.WithServiceAccountTokenPath(spec.ServiceAccountTokenPath))
	}
	k8sAuth, err := auth.NewKubernetesAuth(spec.Role, opts...)
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

	return &Vault{
		cr: cr,
		c:  client,
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
			continue
		}
	}
	return shouldUpdate, nil
}

func (v *Vault) getUsersSecret(ctx context.Context) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}

	spec := v.cr.Spec.Secrets.VaultSpec
	secret, err := v.c.KVv2("secret").Get(ctx, path.Join("psmdb", spec.Role, v.cr.Namespace, v.cr.Name, "users"))
	if err != nil {
		if errors.Is(err, vault.ErrSecretNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("unable to read secret: %w", err)
	}
	return secret.Data, nil
}
