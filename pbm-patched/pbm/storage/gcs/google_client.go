package gcs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	storagegcs "cloud.google.com/go/storage"
	"github.com/googleapis/gax-go/v2"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

type googleClient struct {
	bucketHandle *storagegcs.BucketHandle
	cfg          *Config
	log          log.LogEvent
}

func newGoogleClient(cfg *Config, l log.LogEvent) (*googleClient, error) {
	ctx := context.Background()

	var cli *storagegcs.Client
	var err error

	if cfg.Credentials.PrivateKey != "" && cfg.Credentials.ClientEmail != "" {
		// Use explicit service account JSON credentials
		creds, marshalErr := json.Marshal(ServiceAccountCredentials{
			Type:                "service_account",
			PrivateKey:          cfg.Credentials.PrivateKey,
			ClientEmail:         cfg.Credentials.ClientEmail,
			AuthURI:             "https://accounts.google.com/o/oauth2/auth",
			TokenURI:            "https://oauth2.googleapis.com/token",
			UniverseDomain:      "googleapis.com",
			AuthProviderCertURL: "https://www.googleapis.com/oauth2/v1/certs",
			ClientCertURL: fmt.Sprintf(
				"https://www.googleapis.com/robot/v1/metadata/x509/%s",
				cfg.Credentials.ClientEmail,
			),
		})
		if marshalErr != nil {
			return nil, errors.Wrap(marshalErr, "marshal GCS credentials")
		}

		cli, err = storagegcs.NewClient(ctx, option.WithCredentialsJSON(creds))
		if err != nil {
			return nil, errors.Wrap(err, "new GCS client with JSON credentials")
		}
	} else {
		// Fall back to Application Default Credentials (ADC).
		// On GKE with Workload Identity, the pod's KSA is federated to a GSA,
		// and the GCS client authenticates transparently via the metadata server.
		cli, err = storagegcs.NewClient(ctx)
		if err != nil {
			return nil, errors.Wrap(err, "new GCS client with ADC (workload identity)")
		}
	}

	bh := cli.Bucket(cfg.Bucket).
		Retryer(
			storagegcs.WithBackoff(gax.Backoff{
				Initial:    cfg.Retryer.BackoffInitial,
				Max:        cfg.Retryer.BackoffMax,
				Multiplier: cfg.Retryer.BackoffMultiplier,
			}),
			storagegcs.WithMaxAttempts(cfg.Retryer.MaxAttempts),
			storagegcs.WithPolicy(storagegcs.RetryAlways),
			storagegcs.WithErrorFunc(shouldRetryExtended),
		)

	return &googleClient{
		bucketHandle: bh,
		cfg:          cfg,
		log:          l,
	}, nil
}

// shouldRetryExtended extends default shouldRetry with mainly
// `client connection lost` error from std library's http package.
func shouldRetryExtended(err error) bool {
	if err == nil {
		return false
	}
	if storagegcs.ShouldRetry(err) {
		return true
	}
	if strings.Contains(err.Error(), "http2: client connection lost") ||
		strings.Contains(err.Error(), "connect: network is unreachable") {
		return true
	}

	return false
}

func (g googleClient) save(name string, data io.Reader, options ...storage.Option) error {
	opts := storage.GetDefaultOpts()
	for _, opt := range options {
		if err := opt(opts); err != nil {
			return errors.Wrap(err, "processing options for save")
		}
	}

	const align int64 = 256 << 10 // 256 KiB (both min size and alignment)

	partSize := storage.ComputePartSize(
		opts.Size,
		defaultChunkSize,
		align,
		10_000,
		int64(g.cfg.ChunkSize),
	)

	if rem := partSize % align; rem != 0 {
		partSize += align - rem
	}

	if g.log != nil && opts.UseLogger {
		g.log.Debug(`uploading %q [size hint: %v (%v); part size: %v (%v)]`,
			name,
			opts.Size, storage.PrettySize(opts.Size),
			partSize, storage.PrettySize(partSize))
	}

	ctx := context.Background()
	w := g.bucketHandle.Object(path.Join(g.cfg.Prefix, name)).NewWriter(ctx)
	w.ChunkSize = int(partSize)
	w.ChunkRetryDeadline = g.cfg.Retryer.ChunkRetryDeadline
	if g.log != nil && opts.UseLogger {
		w.ProgressFunc = func(written int64) {
			if opts.Size > 0 {
				g.log.Debug("uploaded %v / %v (%.1f%%)",
					written, opts.Size,
					float64(written)*100/float64(opts.Size))
			} else {
				g.log.Debug("uploaded %v (total unknown)", written)
			}
		}
	}

	if _, err := io.Copy(w, data); err != nil {
		return errors.Wrap(err, "save data")
	}

	if err := w.Close(); err != nil {
		return errors.Wrap(err, "writer close")
	}

	return nil
}

func (g googleClient) fileStat(name string) (storage.FileInfo, error) {
	ctx := context.Background()

	attrs, err := g.bucketHandle.Object(path.Join(g.cfg.Prefix, name)).Attrs(ctx)
	if err != nil {
		if errors.Is(err, storagegcs.ErrObjectNotExist) {
			return storage.FileInfo{}, storage.ErrNotExist
		}

		return storage.FileInfo{}, errors.Wrap(err, "get properties")
	}

	inf := storage.FileInfo{
		Name: attrs.Name,
		Size: attrs.Size,
	}

	if inf.Size == 0 {
		return inf, storage.ErrEmpty
	}

	return inf, nil
}

func (g googleClient) list(prefix, suffix string) ([]storage.FileInfo, error) {
	ctx := context.Background()

	var files []storage.FileInfo
	it := g.bucketHandle.Objects(ctx, &storagegcs.Query{Prefix: prefix})

	for {
		attrs, err := it.Next()

		if errors.Is(err, iterator.Done) {
			break
		}

		if err != nil {
			return nil, errors.Wrap(err, "list objects")
		}

		name := attrs.Name
		name = strings.TrimPrefix(name, prefix)
		if len(name) == 0 {
			continue
		}
		if name[0] == '/' {
			name = name[1:]
		}

		if suffix != "" && !strings.HasSuffix(name, suffix) {
			continue
		}

		files = append(files, storage.FileInfo{
			Name: name,
			Size: attrs.Size,
		})
	}

	return files, nil
}

func (g googleClient) delete(name string) error {
	ctx := context.Background()

	err := g.bucketHandle.Object(path.Join(g.cfg.Prefix, name)).Delete(ctx)
	if err != nil {
		if errors.Is(err, storagegcs.ErrObjectNotExist) {
			return storage.ErrNotExist
		}
		return errors.Wrap(err, "delete object")
	}

	return nil
}

func (g googleClient) copy(src, dst string) error {
	ctx := context.Background()

	srcObj := g.bucketHandle.Object(path.Join(g.cfg.Prefix, src))
	dstObj := g.bucketHandle.Object(path.Join(g.cfg.Prefix, dst))

	_, err := g.fileStat(src)
	if err == storage.ErrNotExist {
		return err
	}

	_, err = dstObj.CopierFrom(srcObj).Run(ctx)
	return err
}

func (g googleClient) getPartialObject(name string, buf *storage.Arena, start, length int64) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
	defer cancel()

	obj := g.bucketHandle.Object(path.Join(g.cfg.Prefix, name))
	reader, err := obj.NewRangeReader(ctx, start, length)
	if err != nil {
		if errors.Is(err, storagegcs.ErrObjectNotExist) || isRangeNotSatisfiable(err) {
			return nil, io.EOF
		}

		return nil, storage.GetObjError{Err: err}
	}

	ch := buf.GetSpan()
	_, err = io.CopyBuffer(ch, reader, buf.CpBuf)
	if err != nil {
		ch.Close()
		return nil, errors.Wrap(err, "copy")
	}
	reader.Close()
	return ch, nil
}
