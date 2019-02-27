package reader

import (
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/golang/snappy"
	"github.com/percona/percona-backup-mongodb/internal/awsutils"
	pb "github.com/percona/percona-backup-mongodb/proto/messages"
	"github.com/percona/percona-backup-mongodb/storage"
	"github.com/pierrec/lz4"
	"github.com/pkg/errors"
)

type BackupReader struct {
	readers   []io.ReadCloser
	bytesRead int64
	lastError error
}

type flusher interface {
	Flush() error
}

func (br *BackupReader) Close() error {
	var err error
	for i := len(br.readers) - 1; i >= 0; i-- {
		if _, ok := br.readers[i].(flusher); ok {
			if err = br.readers[i].(flusher).Flush(); err != nil {
				fmt.Printf("Cannot flush %s", err)
				break
			}
		}
		if err = br.readers[i].Close(); err != nil {
			break
		}
	}
	return nil
}

func (br *BackupReader) Read(buf []byte) (int, error) {
	n, err := br.readers[len(br.readers)-1].Read(buf)
	if err != nil {
		br.readers[len(br.readers)-1].Close()
	}
	return n, err
}

func (br *BackupReader) BytesRead() int64 {
	return br.bytesRead
}

func (br *BackupReader) LastError() error {
	return br.lastError
}

func MakeReader(name string, stg storage.Storage, compressionType pb.CompressionType,
	cypher pb.Cypher) (*BackupReader, error) {
	br := &BackupReader{
		readers: []io.ReadCloser{},
	}

	switch strings.ToLower(stg.Type) {
	case "filesystem":
		filename := filepath.Join(stg.Filesystem.Path, name)
		r, err := os.Open(filename)
		if err != nil {
			return nil, errors.Wrapf(err, "Cannot open dump file %s", filename)
		}
		br.readers = append(br.readers, r)
	case "s3":
		sess, err := awsutils.GetAWSSessionFromStorage(stg.S3)
		if err != nil {
			return nil, errors.Wrapf(err, "Cannot start an S3 session")
		}
		s3Svc := s3.New(sess)
		result, err := s3Svc.GetObject(&s3.GetObjectInput{
			Bucket: aws.String(stg.S3.Bucket),
			Key:    aws.String(name),
		})

		if err != nil {
			return nil, errors.Wrapf(err, "Cannot read backup file %s from bucket %s", name, stg.S3.Bucket)
		}

		// we don't want that the body gets closed when the reader is depleated otherwise it we be
		// closed too soon
		br.readers = append(br.readers, ioutil.NopCloser(result.Body))
	default:
		return nil, fmt.Errorf("Don't know how to handle %q storage type", stg.Type)
	}

	switch compressionType {
	case pb.CompressionType_COMPRESSION_TYPE_GZIP:
		gzr, err := gzip.NewReader(br.readers[len(br.readers)-1])
		if err != nil {
			return nil, errors.Wrap(err, "cannot create a gzip reader")
		}
		br.readers = append(br.readers, gzr)
	case pb.CompressionType_COMPRESSION_TYPE_LZ4:
		lz4r := lz4.NewReader(br.readers[len(br.readers)-1])
		br.readers = append(br.readers, ioutil.NopCloser(lz4r))
	case pb.CompressionType_COMPRESSION_TYPE_SNAPPY:
		snappyr := snappy.NewReader(br.readers[len(br.readers)-1])
		br.readers = append(br.readers, ioutil.NopCloser(snappyr))
	}

	return br, nil

}
