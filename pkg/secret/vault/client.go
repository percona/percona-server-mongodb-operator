package vault

import (
	"bytes"
	"context"
	"net/url"
	"path"
	"strings"

	vault "github.com/hashicorp/vault/api"
	auth "github.com/hashicorp/vault/api/auth/kubernetes"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/secret"
)

type vaultClient struct {
	c kvClient

	mountPath string
	keyPath   string
}

func newClient(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) (*vaultClient, error) {
	spec := cr.Spec.VaultSpec
	if spec.EndpointURL == "" {
		return nil, nil
	}
	_, err := url.Parse(spec.EndpointURL)
	if err != nil {
		return nil, secret.NewCriticalErr(errors.Wrap(err, "failed to parse endpointURL"))
	}

	config := vault.DefaultConfig()
	config.Address = spec.EndpointURL

	if spec.TLSSecret != "" {
		sec := new(corev1.Secret)
		if err := cl.Get(ctx, types.NamespacedName{
			Name:      spec.TLSSecret,
			Namespace: cr.Namespace,
		}, sec); err != nil {
			werr := errors.Wrap(err, "get vault tls secret")
			if k8serrors.IsNotFound(err) {
				return nil, secret.NewCriticalErr(werr)
			}
			return nil, werr
		}

		ca, ok := sec.Data["ca.crt"]
		if !ok {
			return nil, secret.NewCriticalErr(errors.New("tls secret does not have ca.crt key"))
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
			werr := errors.Wrap(err, "failed to get tokenSecret")
			if k8serrors.IsNotFound(err) {
				return nil, secret.NewCriticalErr(werr)
			}
			return nil, werr
		}
		if _, ok := tokenSecret.Data["token"]; !ok {
			return nil, secret.NewCriticalErr(errors.New("expected `token` key is not present in the .syncUsers.tokenSecret data"))
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
	return &vaultClient{
		keyPath:   keyPath,
		mountPath: mountPath,
		c:         &rawClient{c: client},
	}, nil
}

func (v *vaultClient) FillSecretData(ctx context.Context, data map[string][]byte) (bool, error) {
	if v == nil {
		return false, nil
	}

	vaultData, err := v.getUsersSecret(ctx)
	if err != nil {
		return false, errors.Wrap(err, "get users secret")
	}

	if len(vaultData) == 0 {
		log := logf.FromContext(ctx)
		log.Info("no data found in Vault", "keyPath", v.keyPath, "mountPath", v.mountPath)
		return false, nil
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

func (v *vaultClient) getUsersSecret(ctx context.Context) (map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	log := logf.FromContext(ctx)

	sec, err := v.c.KVv2(v.mountPath).Get(ctx, v.keyPath)
	if errors.Is(err, vault.ErrSecretNotFound) {
		log.Info("secret is not found in the vault", "mountPath", v.mountPath, "keyPath", v.keyPath)
		return nil, nil
	} else if err != nil && strings.Contains(err.Error(), "configured Vault token contains non-printable characters and cannot be used") {
		return nil, secret.NewCriticalErr(err)
	} else if err != nil {
		return nil, errors.Wrap(err, "unable to read secret")
	}
	return sec.Data, nil
}
