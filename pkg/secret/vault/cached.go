package vault

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"time"

	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

type cachedClient struct {
	hash []byte

	lastUpdatedAt  time.Time
	reinitInterval time.Duration

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

	cv.vaultClient, err = newClient(ctx, cl, cr)
	if err != nil {
		return errors.Wrap(err, "new vault")
	}

	cv.lastUpdatedAt = time.Now()

	return nil
}

func (cv *cachedClient) updateHash(cr *api.PerconaServerMongoDB) (bool, error) {
	newHash, err := vaultSpecHash(cr)
	if err != nil {
		return false, err
	}
	changed := !bytes.Equal(newHash, cv.hash)
	cv.hash = newHash
	return changed, nil
}

func vaultSpecHash(cr *api.PerconaServerMongoDB) ([]byte, error) {
	data, err := json.Marshal(cr.Spec.VaultSpec)
	if err != nil {
		return nil, errors.Wrap(err, "marshal")
	}

	hash := md5.Sum(data)
	return hash[:], nil
}
