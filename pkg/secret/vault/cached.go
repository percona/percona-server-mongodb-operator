package vault

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"hash"
	"sort"
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

	changed, err := cv.updateHash(ctx, cl, cr)
	if err != nil {
		return errors.Wrap(err, "update hash")
	}
	if !changed && time.Since(cv.lastUpdatedAt) <= reinitInterval {
		return nil
	}

	cv.vaultClient, err = newClient(ctx, cl, cr)
	if err != nil {
		return errors.Wrap(err, "new vault")
	}

	cv.lastUpdatedAt = time.Now()

	return nil
}

func (cv *cachedClient) updateHash(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) (bool, error) {
	newHash, err := vaultSpecHash(ctx, cl, cr)
	if err != nil {
		return false, err
	}
	changed := !bytes.Equal(newHash, cv.hash)
	cv.hash = newHash
	return changed, nil
}

func vaultSpecHash(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) ([]byte, error) {
	data, err := json.Marshal(cr.Spec.VaultSpec)
	if err != nil {
		return nil, errors.Wrap(err, "marshal")
	}

	h := md5.New()
	h.Write(data)

	if err := writeTokenSecretHash(ctx, cl, cr, h); err != nil {
		return nil, err
	}

	return h.Sum(nil), nil
}

func writeTokenSecretHash(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, h hash.Hash) error {
	tokenSecretName := cr.Spec.VaultSpec.SyncUsersSpec.TokenSecret
	if tokenSecretName == "" {
		return nil
	}

	sec := new(corev1.Secret)
	err := cl.Get(ctx, types.NamespacedName{
		Name:      tokenSecretName,
		Namespace: cr.Namespace,
	}, sec)
	if k8serrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "get vault token secret")
	}

	keys := make([]string, 0, len(sec.Data))
	for k := range sec.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write(sec.Data[k])
	}
	return nil
}
