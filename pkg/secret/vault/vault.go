package vault

import (
	"context"

	vault "github.com/hashicorp/vault/api"

	"github.com/percona/percona-server-mongodb-operator/pkg/secret"
)

type kvClient interface {
	KVv2(mountPath string) kvReader
}

type kvReader interface {
	Get(ctx context.Context, path string) (*vault.KVSecret, error)
}

type rawClient struct {
	c *vault.Client
}

func (r *rawClient) KVv2(mountPath string) kvReader {
	return &rawReader{kv: r.c.KVv2(mountPath)}
}

type rawReader struct {
	kv *vault.KVv2
}

func (r *rawReader) Get(ctx context.Context, path string) (*vault.KVSecret, error) {
	return r.kv.Get(ctx, path)
}

type Provider struct{}

func (p *Provider) NewClient() secret.Client {
	return new(cachedClient)
}
