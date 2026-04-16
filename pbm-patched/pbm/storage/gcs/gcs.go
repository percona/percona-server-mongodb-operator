package gcs

import (
	"io"
	"path"
	"strings"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

const (
	gcsEndpointURL = "storage.googleapis.com"

	defaultChunkSize    = 10 * 1024 * 1024 // 10MiB
	defaultMaxObjSizeGB = 5018             // 4.9 TB

	defaultMaxAttempts        = 5
	defaultBackoffInitial     = time.Second
	defaultBackoffMax         = 30 * time.Second
	defaultBackoffMultiplier  = 2
	defaultChunkRetryDeadline = 32 * time.Second
)

type ServiceAccountCredentials struct {
	Type                string `json:"type"`
	PrivateKey          string `json:"private_key"`
	ClientEmail         string `json:"client_email"`
	AuthURI             string `json:"auth_uri"`
	TokenURI            string `json:"token_uri"`
	UniverseDomain      string `json:"universe_domain"`
	AuthProviderCertURL string `json:"auth_provider_x509_cert_url"`
	ClientCertURL       string `json:"client_x509_cert_url"`
}

type gcsClient interface {
	save(name string, data io.Reader, options ...storage.Option) error
	fileStat(name string) (storage.FileInfo, error)
	list(prefix, suffix string) ([]storage.FileInfo, error)
	delete(name string) error
	copy(src, dst string) error
	getPartialObject(name string, buf *storage.Arena, start, length int64) (io.ReadCloser, error)
}

type GCS struct {
	cfg *Config
	log log.LogEvent

	client gcsClient
	d      *Download
}

func New(cfg *Config, node string, l log.LogEvent) (storage.Storage, error) {
	if err := cfg.Cast(); err != nil {
		return nil, errors.Wrap(err, "set defaults")
	}

	g := &GCS{
		cfg: cfg,
		log: l,
	}

	if g.cfg.Credentials.HMACAccessKey != "" && g.cfg.Credentials.HMACSecret != "" {
		hc, err := newHMACClient(g.cfg, g.log)
		if err != nil {
			return nil, errors.Wrap(err, "new hmac client")
		}
		g.client = hc
	} else {
		gc, err := newGoogleClient(g.cfg, g.log)
		if err != nil {
			return nil, errors.Wrap(err, "new google client")
		}
		g.client = gc
	}

	g.d = &Download{
		arenas:   []*storage.Arena{storage.NewArena(storage.DownloadChuckSizeDefault, storage.DownloadChuckSizeDefault)},
		spanSize: storage.DownloadChuckSizeDefault,
		cc:       1,
	}

	return storage.NewSplitMergeMW(g, cfg.GetMaxObjSizeGB()), nil
}

func NewWithDownloader(
	opts *Config,
	node string,
	l log.LogEvent,
	cc, bufSizeMb, spanSizeMb int,
) (storage.Storage, error) {
	if l == nil {
		l = log.DiscardEvent
	}

	g := &GCS{
		cfg: opts,
		log: l,
	}

	if g.cfg.Credentials.HMACAccessKey != "" && g.cfg.Credentials.HMACSecret != "" {
		hc, err := newHMACClient(g.cfg, g.log)
		if err != nil {
			return nil, errors.Wrap(err, "new hmac client")
		}
		g.client = hc
	} else {
		gc, err := newGoogleClient(g.cfg, g.log)
		if err != nil {
			return nil, errors.Wrap(err, "new google client")
		}
		g.client = gc
	}

	arenaSize, spanSize, cc := storage.DownloadOpts(cc, bufSizeMb, spanSizeMb)
	g.log.Debug("download max buf %d (arena %d, span %d, concurrency %d)", arenaSize*cc, arenaSize, spanSize, cc)

	var arenas []*storage.Arena
	for i := 0; i < cc; i++ {
		arenas = append(arenas, storage.NewArena(arenaSize, spanSize))
	}

	g.d = &Download{
		arenas:   arenas,
		spanSize: spanSize,
		cc:       cc,
		stat:     storage.NewDownloadStat(cc, arenaSize, spanSize),
	}

	return storage.NewSplitMergeMW(g, opts.GetMaxObjSizeGB()), nil
}

func (*GCS) Type() storage.Type {
	return storage.GCS
}

func (g *GCS) Save(name string, data io.Reader, options ...storage.Option) error {
	return g.client.save(name, data, options...)
}

func (g *GCS) FileStat(name string) (storage.FileInfo, error) {
	return g.client.fileStat(name)
}

func (g *GCS) List(prefix, suffix string) ([]storage.FileInfo, error) {
	prfx := path.Join(g.cfg.Prefix, prefix)

	if prfx != "" && !strings.HasSuffix(prfx, "/") {
		prfx += "/"
	}

	return g.client.list(prfx, suffix)
}

func (g *GCS) Delete(name string) error {
	return g.client.delete(name)
}

func (g *GCS) Copy(src, dst string) error {
	return g.client.copy(src, dst)
}
