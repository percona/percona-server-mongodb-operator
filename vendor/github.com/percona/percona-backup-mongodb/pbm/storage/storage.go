package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
)

var (
	// ErrNotExist is an error for file doesn't exists on storage
	ErrNotExist      = errors.New("no such file")
	ErrEmpty         = errors.New("file is empty")
	ErrUninitialized = errors.New("uninitialized")
)

// Type represents a type of the destination storage for backups
type Type string

const (
	Undefined  Type = ""
	S3         Type = "s3"
	Azure      Type = "azure"
	Filesystem Type = "filesystem"
	Blackhole  Type = "blackhole"
)

type FileInfo struct {
	Name string // with path
	Size int64
}

type Storage interface {
	Type() Type
	Save(name string, data io.Reader, size int64) error
	SourceReader(name string) (io.ReadCloser, error)
	// FileStat returns file info. It returns error if file is empty or not exists.
	FileStat(name string) (FileInfo, error)
	// List scans path with prefix and returns all files with given suffix.
	// Both prefix and suffix can be omitted.
	List(prefix, suffix string) ([]FileInfo, error)
	// Delete deletes given file.
	// It returns storage.ErrNotExist if a file doesn't exists.
	Delete(name string) error
	// Copy makes a copy of the src objec/file under dst name
	Copy(src, dst string) error
}

// ParseType parses string and returns storage type
func ParseType(s string) Type {
	switch s {
	case string(S3):
		return S3
	case string(Azure):
		return Azure
	case string(Filesystem):
		return Filesystem
	case string(Blackhole):
		return Blackhole
	default:
		return Undefined
	}
}

// IsInitialized checks if there is PBM init file on the storage.
func IsInitialized(ctx context.Context, stg Storage) (bool, error) {
	_, err := stg.FileStat(defs.StorInitFile)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return false, nil
		}

		return false, errors.Wrap(err, "file stat")
	}

	return true, nil
}

// HasReadAccess checks if the provided storage allows the reading of file content.
//
// It gets the size (stat) and reads the content of the PBM init file.
//
// ErrUninitialized is returned if there is no init file.
func HasReadAccess(ctx context.Context, stg Storage) error {
	stat, err := stg.FileStat(defs.StorInitFile)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return ErrUninitialized
		}

		return errors.Wrap(err, "file stat")
	}

	r, err := stg.SourceReader(defs.StorInitFile)
	if err != nil {
		return errors.Wrap(err, "open file")
	}
	defer func() {
		err := r.Close()
		if err != nil {
			log.LogEventFromContext(ctx).
				Error("HasReadAccess(): close file: %v", err)
		}
	}()

	const MaxCount = 10 // for "v999.99.99"
	var buf [MaxCount]byte
	n, err := r.Read(buf[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return errors.Wrap(err, "read file")
	}

	expect := MaxCount
	if stat.Size < int64(expect) {
		expect = int(stat.Size)
	}
	if n != expect {
		return errors.Errorf("short read (%d of %d)", n, expect)
	}

	return nil
}

// rwError multierror for the read/compress/write-to-store operations set
type rwError struct {
	read     error
	compress error
	write    error
}

func (rwe rwError) Error() string {
	var r string
	if rwe.read != nil {
		r += "read data: " + rwe.read.Error() + "."
	}
	if rwe.compress != nil {
		r += "compress data: " + rwe.compress.Error() + "."
	}
	if rwe.write != nil {
		r += "write data: " + rwe.write.Error() + "."
	}

	return r
}

func (rwe rwError) Unwrap() error {
	if rwe.read != nil {
		return rwe.read
	}
	if rwe.write != nil {
		return rwe.write
	}
	if rwe.compress != nil {
		return rwe.compress
	}
	return nil
}

func (rwe rwError) nil() bool {
	return rwe.read == nil && rwe.compress == nil && rwe.write == nil
}

type Source interface {
	io.WriterTo
}

type Canceller interface {
	Cancel()
}

// ErrCancelled means backup was canceled
var ErrCancelled = errors.New("backup canceled")

// Upload writes data to dst from given src and returns an amount of written bytes
func Upload(
	ctx context.Context,
	src Source,
	dst Storage,
	compression compress.CompressionType,
	compressLevel *int,
	fname string,
	sizeb int64,
) (int64, error) {
	r, pw := io.Pipe()

	w, err := compress.Compress(pw, compression, compressLevel)
	if err != nil {
		return 0, err
	}

	var rwErr rwError
	var n int64
	go func() {
		n, rwErr.read = src.WriteTo(w)
		rwErr.compress = w.Close()
		pw.Close()
	}()

	saveDone := make(chan struct{})
	go func() {
		rwErr.write = dst.Save(fname, r, sizeb)
		saveDone <- struct{}{}
	}()

	select {
	case <-ctx.Done():
		if c, ok := src.(Canceller); ok {
			c.Cancel()
		}

		err := r.Close()
		if err != nil {
			return 0, errors.Wrap(err, "cancel upload: close reader")
		}
		return 0, ErrCancelled
	case <-saveDone:
	}

	r.Close()

	if !rwErr.nil() {
		return 0, rwErr
	}

	return n, nil
}

func PrettySize(size int64) string {
	const (
		_          = iota
		KB float64 = 1 << (10 * iota)
		MB
		GB
		TB
	)

	if size < 0 {
		return "unknown"
	}

	s := float64(size)

	switch {
	case s >= TB:
		return fmt.Sprintf("%.2fTB", s/TB)
	case s >= GB:
		return fmt.Sprintf("%.2fGB", s/GB)
	case s >= MB:
		return fmt.Sprintf("%.2fMB", s/MB)
	case s >= KB:
		return fmt.Sprintf("%.2fKB", s/KB)
	}
	return fmt.Sprintf("%.2fB", s)
}
