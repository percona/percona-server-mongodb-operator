package storage

import (
	"bytes"
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
	GCS        Type = "gcs"
	Minio      Type = "minio"
	OSS        Type = "oss"
)

type FileInfo struct {
	Name string // with path
	Size int64
}

type Storage interface {
	Type() Type
	Save(name string, data io.Reader, options ...Option) error
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
	// Get download stat
	DownloadStat() DownloadStat
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
	case string(GCS):
		return GCS
	case string(Minio):
		return Minio
	case string(OSS):
		return OSS
	default:
		return Undefined
	}
}

// Opts represents storage options
type Opts struct {
	UseLogger bool
	Size      int64
}

// GetDefaultOpts creates default options.
func GetDefaultOpts() *Opts {
	return &Opts{
		UseLogger: true,
		Size:      -1,
	}
}

// Option is function for setting the storage option
type Option func(*Opts) error

// UseLogger option enables/disables logger when working with storage.
// Logger is enabled by default.
func UseLogger(useLogger bool) Option {
	return func(o *Opts) error {
		o.UseLogger = useLogger
		return nil
	}
}

// Size option sets size of the file for the upload.
// It's used to calculate params for multi-part upload.
func Size(size int64) Option {
	return func(o *Opts) error {
		if size < 0 && size != -1 {
			return errors.New("invalid size option value")
		}
		o.Size = size
		return nil
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
		rwErr.write = dst.Save(fname, r, Size(sizeb))
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

// ComputePartSize calculates the optimal chunk size for uploading a large file,
// depending on the expected total size and backend constraints.
//
// It's intended for backends that support multipart or resumable uploads:
//   - AWS S3 (multipart upload with part count limits)
//   - GCS XML API (via MinIO, with HMAC credentials)
//   - GCS JSON API (resumable upload, used for Writer.ChunkSize tuning)
//
// Behavior:
//   - Starts with a backend-specific `defaultChunk` (e.g., 10 MiB).
//   - If a user-provided size is specified, it overrides the default (but not below minSize).
//   - If fileSize is known, computes a chunk size large enough to keep the
//     total number of chunks below maxParts (typically 10,000).
//   - The chunk size is scaled by a 1.5x safety factor to ensure a margin.
//
// This function ensures that uploads stay within service limits.
// For example, AWS S3 and GCS XML APIs have a hard cap of 10,000 parts, so:
//   - With a 10 MiB part size, the max supported file size is ~97.6 GiB.
//   - Larger files must use larger part sizes to stay under the limit.
//
// For resumable APIs like GCS JSON, there is no hard part-count limit,
// but this function still helps determine an efficient flush interval.
func ComputePartSize(fileSize, defaultSize, minSize, maxParts, userSize int64) int64 {
	partSize := defaultSize

	if userSize > 0 {
		partSize = userSize
		partSize = max(partSize, minSize)
	}

	if fileSize > 0 {
		ps := fileSize / maxParts * 15 / 10 // 50% headroom
		if ps > partSize {
			partSize = ps
		}
	}

	return partSize
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

type RetryableError struct {
	Err error
}

func (e *RetryableError) Error() string {
	return e.Err.Error()
}

func IsRetryableError(err error) bool {
	var e *RetryableError
	return errors.As(err, &e)
}

func RetryableWrite(stg Storage, name string, data []byte) error {
	err := stg.Save(name, bytes.NewBuffer(data), Size(int64(len(data))))
	if err != nil && stg.Type() == Filesystem {
		if IsRetryableError(err) {
			err = stg.Save(name, bytes.NewBuffer(data), Size(int64(len(data))))
		}
	}

	return err
}
