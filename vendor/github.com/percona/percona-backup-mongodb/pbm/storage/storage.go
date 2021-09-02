package storage

import (
	"errors"
	"io"
)

var (
	// ErrNotExist is an error for file doesn't exists on storage
	ErrNotExist = errors.New("no such file")
	ErrEmpty    = errors.New("file is empty")
)

type FileInfo struct {
	Name string // with path
	Size int64
}

type Storage interface {
	Save(name string, data io.Reader, size int) error
	SourceReader(name string) (io.ReadCloser, error)
	// FileStat returns file info. It returns error if file is empty or not exists.
	FileStat(name string) (FileInfo, error)
	// List scans path with prefix and returns all files with given suffix.
	// Both prefix and suffix can be omitted.
	List(prefix, suffix string) ([]FileInfo, error)
	// Delete deletes given file.
	// It returns storage.ErrNotExist if a file doesn't exists.
	Delete(name string) error
}
