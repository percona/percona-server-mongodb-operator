package vault

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"hash"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

const defaultReinitInterval = 30 * time.Minute

type cachedClient struct {
	hash []byte

	lastUpdatedAt time.Time

	*vaultClient
}

func (cv *cachedClient) Name() string {
	return "vault"
}

func (cv *cachedClient) Close() error {
	return nil
}

func (cv *cachedClient) Update(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) error {
	if cv == nil || cr.Spec.VaultSpec == nil || cr.Spec.VaultSpec.EndpointURL == "" {
		return nil
	}

	reinitInterval := defaultReinitInterval
	if cr.Spec.VaultSpec.ReinitInterval != nil {
		reinitInterval = cr.Spec.VaultSpec.ReinitInterval.Duration
	}

	newHash, err := vaultSpecHash(ctx, cl, cr)
	if err != nil {
		return errors.Wrap(err, "update hash")
	}
	changed := !bytes.Equal(newHash, cv.hash)
	if !changed && time.Since(cv.lastUpdatedAt) <= reinitInterval {
		return nil
	}

	cv.vaultClient, err = newClient(ctx, cl, cr)
	if err != nil {
		return errors.Wrap(err, "new vault")
	}

	cv.hash = newHash
	cv.lastUpdatedAt = time.Now()

	return nil
}

func vaultSpecHash(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) ([]byte, error) {
	data, err := json.Marshal(cr.Spec.VaultSpec)
	if err != nil {
		return nil, errors.Wrap(err, "marshal")
	}

	h := md5.New()
	h.Write(data)

	if err := writeSecretHash(ctx, cl, cr.Namespace, cr.Spec.VaultSpec.SyncUsersSpec.TokenSecret, h); err != nil {
		return nil, errors.Wrap(err, "write token secret hash")
	}
	if err := writeSecretHash(ctx, cl, cr.Namespace, cr.Spec.VaultSpec.TLSSecret, h); err != nil {
		return nil, errors.Wrap(err, "write tls secret hash")
	}

	return h.Sum(nil), nil
}

func writeSecretHash(ctx context.Context, cl client.Client, namespace, secretName string, h hash.Hash) error {
	if secretName == "" {
		return nil
	}

	sec := new(corev1.Secret)
	err := cl.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: namespace,
	}, sec)
	if k8serrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "get secret")
	}

	data, err := json.Marshal(sec.Data)
	if err != nil {
		return errors.Wrap(err, "marshal secret data")
	}
	h.Write([]byte(secretName))
	h.Write(data)
	return nil
}
