package blackhole

import (
	"io"
	"io/ioutil"
)

type Blackhole struct{}

func New() *Blackhole {
	return &Blackhole{}
}

func (b *Blackhole) Save(_ string, data io.Reader) error {
	_, err := io.Copy(ioutil.Discard, data)
	return err
}

func (b *Blackhole) FilesList(_ string) ([][]byte, error) { return [][]byte{}, nil }
func (b *Blackhole) Delete(name string) error             { return nil }

// NopReadCloser is a no operation ReadCloser
type NopReadCloser struct{}

func (NopReadCloser) Read(b []byte) (int, error) {
	return len(b), nil
}
func (NopReadCloser) Close() error { return nil }

func (b *Blackhole) SourceReader(name string) (io.ReadCloser, error) { return NopReadCloser{}, nil }
