package storage

import (
	"io"
)

type Storage interface {
	Save(name string, data io.Reader) error
	SourceReader(name string) (io.ReadCloser, error)
	FilesList(suffix string) ([][]byte, error)
	Delete(name string) error
}
