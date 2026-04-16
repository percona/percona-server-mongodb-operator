package blackhole

import (
	"io"

	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

type Blackhole struct{}

var _ storage.Storage = &Blackhole{}

func New() *Blackhole {
	return &Blackhole{}
}

func (*Blackhole) Type() storage.Type {
	return storage.Blackhole
}

func (*Blackhole) Save(_ string, data io.Reader, _ ...storage.Option) error {
	_, err := io.Copy(io.Discard, data)
	return err
}

func (*Blackhole) List(_, _ string) ([]storage.FileInfo, error) { return []storage.FileInfo{}, nil }
func (*Blackhole) Delete(_ string) error                        { return nil }
func (*Blackhole) FileStat(_ string) (storage.FileInfo, error)  { return storage.FileInfo{}, nil }
func (*Blackhole) Copy(_, _ string) error                       { return nil }
func (*Blackhole) DownloadStat() storage.DownloadStat           { return storage.DownloadStat{} }

// NopReadCloser is a no operation ReadCloser
type NopReadCloser struct{}

func (NopReadCloser) Read(b []byte) (int, error) {
	return len(b), nil
}
func (NopReadCloser) Close() error { return nil }

func (*Blackhole) SourceReader(name string) (io.ReadCloser, error) { return NopReadCloser{}, nil }
