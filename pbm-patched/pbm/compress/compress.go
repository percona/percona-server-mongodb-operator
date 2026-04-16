package compress

import (
	"compress/gzip"
	"io"
	"runtime"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/golang/snappy"
	"github.com/klauspost/compress/s2"
	"github.com/klauspost/compress/zstd"
	"github.com/klauspost/pgzip"
	"github.com/pierrec/lz4"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

type CompressionType string

const (
	CompressionTypeNone      CompressionType = "none"
	CompressionTypeGZIP      CompressionType = "gzip"
	CompressionTypePGZIP     CompressionType = "pgzip"
	CompressionTypeSNAPPY    CompressionType = "snappy"
	CompressionTypeLZ4       CompressionType = "lz4"
	CompressionTypeS2        CompressionType = "s2"
	CompressionTypeZstandard CompressionType = "zstd"
)

func (c CompressionType) Suffix() string {
	switch c {
	case CompressionTypeGZIP, CompressionTypePGZIP:
		return ".gz"
	case CompressionTypeLZ4:
		return ".lz4"
	case CompressionTypeSNAPPY:
		return ".snappy"
	case CompressionTypeS2:
		return ".s2"
	case CompressionTypeZstandard:
		return ".zst"
	case CompressionTypeNone:
		fallthrough
	default:
		return ""
	}
}

func IsValidCompressionType(s string) bool {
	switch CompressionType(s) {
	case
		CompressionTypeNone,
		CompressionTypeGZIP,
		CompressionTypePGZIP,
		CompressionTypeSNAPPY,
		CompressionTypeLZ4,
		CompressionTypeS2,
		CompressionTypeZstandard:
		return true
	}

	return false
}

// FileCompression return compression alg based on given file extension
func FileCompression(ext string) CompressionType {
	switch ext {
	default:
		return CompressionTypeNone
	case "gz":
		return CompressionTypeGZIP
	case "lz4":
		return CompressionTypeLZ4
	case "snappy":
		return CompressionTypeSNAPPY
	case "s2":
		return CompressionTypeS2
	case "zst":
		return CompressionTypeZstandard
	}
}

// Compress makes a compressed writer from the given one
func Compress(w io.Writer, compression CompressionType, level *int) (io.WriteCloser, error) {
	switch compression {
	case CompressionTypeGZIP:
		if level == nil {
			level = aws.Int(gzip.DefaultCompression)
		}
		gw, err := gzip.NewWriterLevel(w, *level)
		if err != nil {
			return nil, err
		}
		return gw, nil
	case CompressionTypePGZIP:
		if level == nil {
			level = aws.Int(pgzip.DefaultCompression)
		}
		pgw, err := pgzip.NewWriterLevel(w, *level)
		if err != nil {
			return nil, err
		}
		cc := runtime.NumCPU() / 2
		if cc == 0 {
			cc = 1
		}
		err = pgw.SetConcurrency(1<<20, cc)
		if err != nil {
			return nil, err
		}
		return pgw, nil
	case CompressionTypeLZ4:
		lz4w := lz4.NewWriter(w)
		if level != nil {
			lz4w.Header.CompressionLevel = *level
		}
		return lz4w, nil
	case CompressionTypeSNAPPY:
		return snappy.NewBufferedWriter(w), nil
	case CompressionTypeS2:
		cc := runtime.NumCPU() / 3
		if cc == 0 {
			cc = 1
		}
		writerOptions := []s2.WriterOption{s2.WriterConcurrency(cc)}
		if level != nil {
			switch *level {
			case 1:
				writerOptions = append(writerOptions, s2.WriterUncompressed())
			case 3:
				writerOptions = append(writerOptions, s2.WriterBetterCompression())
			case 4:
				writerOptions = append(writerOptions, s2.WriterBestCompression())
			}
		}
		return s2.NewWriter(w, writerOptions...), nil
	case CompressionTypeZstandard:
		encLevel := zstd.SpeedDefault
		if level != nil {
			encLevel = zstd.EncoderLevelFromZstd(*level)
		}
		return zstd.NewWriter(w, zstd.WithEncoderLevel(encLevel))
	case CompressionTypeNone:
		fallthrough
	default:
		return nopWriteCloser{w}, nil
	}
}

// Decompress wraps given reader by the decompressing io.ReadCloser
func Decompress(r io.Reader, c CompressionType) (io.ReadCloser, error) {
	switch c {
	case CompressionTypeGZIP, CompressionTypePGZIP:
		rr, err := gzip.NewReader(r)
		return rr, errors.Wrap(err, "gzip reader")
	case CompressionTypeLZ4:
		return io.NopCloser(lz4.NewReader(r)), nil
	case CompressionTypeSNAPPY:
		return io.NopCloser(snappy.NewReader(r)), nil
	case CompressionTypeS2:
		return io.NopCloser(s2.NewReader(r)), nil
	case CompressionTypeZstandard:
		rr, err := zstd.NewReader(r)
		return io.NopCloser(rr), errors.Wrap(err, "zstandard reader")
	case CompressionTypeNone:
		fallthrough
	default:
		return io.NopCloser(r), nil
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
