package storage

import (
	"errors"
	"io"
)

// ErrNotExist is an error for file isn't exists on storage
var ErrNotExist = errors.New("no such file")

type FileInfo struct {
	Name string // with path
	Size int64
}

type Storage interface {
	Save(name string, data io.Reader, size int) error
	SourceReader(name string) (io.ReadCloser, error)
	// FileStat returns file info. It returns error if file is empty or not exists
	FileStat(name string) (FileInfo, error)
	List(prefix string) ([]FileInfo, error)
	Files(suffix string) ([][]byte, error)
	// Delete deletes given file.
	// It returns storage.ErrNotExist if a file isn't exists
	Delete(name string) error
}
