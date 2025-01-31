package snapshot

import (
	"context"
	"io"
	"sync/atomic"

	"github.com/percona/percona-backup-mongodb/pbm/archive"
	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

type UploadFunc func(ns, ext string, r io.Reader) error

func UploadDump(
	ctx context.Context,
	dump func(archive.NewWriter) error,
	upload UploadFunc,
	compression compress.CompressionType,
	compressionLevel *int,
) (int64, error) {
	uploadSize := int64(0)

	newWriter := func(ns string) (io.WriteCloser, error) {
		pr, pw := io.Pipe()

		compression := compression
		if ns == archive.MetaFileV2 {
			compression = compress.CompressionTypeNone
		}

		done := make(chan error)
		go func() {
			defer close(done)

			rc := &readCounter{r: pr}
			err := upload(ns, compression.Suffix(), rc)
			if err != nil {
				err = errors.Wrapf(err, "upload: %q", ns)
				pr.CloseWithError(err)
				done <- err
			}

			atomic.AddInt64(&uploadSize, rc.n)
		}()

		w, err := compress.Compress(pw, compression, compressionLevel)
		dwc := io.WriteCloser(&delegatedWriteCloser{w, funcCloser(func() error {
			err0 := w.Close()
			err1 := pw.Close()
			err2 := <-done
			return errors.Join(err0, err1, err2)
		})})
		return dwc, errors.Wrapf(err, "create compressor: %q", ns)
	}

	err := dump(newWriter)
	return atomic.LoadInt64(&uploadSize), err
}

type DownloadFunc func(filename string) (io.ReadCloser, error)

func DownloadDump(
	download DownloadFunc,
	compression compress.CompressionType,
	match archive.NSFilterFn,
	numParallelColls int,
) (io.ReadCloser, error) {
	pr, pw := io.Pipe()

	go func() {
		newReader := func(ns string) (io.ReadCloser, error) {
			if ns != archive.MetaFile {
				ns += compression.Suffix()
			}

			r, err := download(ns)
			if err != nil {
				return nil, errors.Wrapf(err, "download: %q", ns)
			}

			if ns == archive.MetaFile {
				return r, nil
			}

			r, err = compress.Decompress(r, compression)
			return r, errors.Wrapf(err, "create decompressor: %q", ns)
		}

		err := archive.Compose(pw, newReader, match, numParallelColls)
		pw.CloseWithError(errors.Wrap(err, "compose"))
	}()

	return pr, nil
}

type readCounter struct {
	r io.Reader
	n int64
}

func (c *readCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

type funcCloser func() error

func (f funcCloser) Close() error {
	return f()
}

type delegatedWriteCloser struct {
	w io.Writer
	c io.Closer
}

func (d *delegatedWriteCloser) Write(b []byte) (int, error) {
	return d.w.Write(b)
}

func (d *delegatedWriteCloser) Close() error {
	return d.c.Close()
}
