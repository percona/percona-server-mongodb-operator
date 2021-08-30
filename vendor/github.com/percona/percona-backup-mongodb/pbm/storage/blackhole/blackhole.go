package blackhole

import (
	"io"
	"io/ioutil"

	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

type Blackhole struct{}

func New() *Blackhole {
	return &Blackhole{}
}

func (*Blackhole) Save(_ string, data io.Reader, _ int) error {
	_, err := io.Copy(ioutil.Discard, data)
	return err
}

func (*Blackhole) List(_, _ string) ([]storage.FileInfo, error)        { return []storage.FileInfo{}, nil }
func (*Blackhole) Delete(_ string) error                               { return nil }
func (*Blackhole) FileStat(_ string) (inf storage.FileInfo, err error) { return }

// NopReadCloser is a no operation ReadCloser
type NopReadCloser struct{}

func (NopReadCloser) Read(b []byte) (int, error) {
	return len(b), nil
}
func (NopReadCloser) Close() error { return nil }

func (*Blackhole) SourceReader(name string) (io.ReadCloser, error) { return NopReadCloser{}, nil }
