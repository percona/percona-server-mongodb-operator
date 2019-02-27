package bsonfile

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/globalsign/mgo/bson"
)

type BSONReader interface {
	ReadNext() ([]byte, error)
	UnmarshalNext(interface{}) error
}

type BSONFile struct {
	fh io.ReadCloser
}

const (
	MaxBSONSize = 16 * 1024 * 1024 // 16MB - maximum BSON document size
)

func NewBSONReader(r io.Reader) (BSONReader, error) {
	if r == nil {
		return nil, fmt.Errorf("The reader cannot be null")
	}
	return &BSONFile{fh: ioutil.NopCloser(r)}, nil
}

// Open opens a bson file for reading
func OpenFile(filename string) (*BSONFile, error) {
	fh, err := os.Open(filepath.Clean(filename))
	if err != nil {
		return nil, err
	}
	return &BSONFile{
		fh: fh,
	}, nil
}

func (b *BSONFile) Close() error {
	return b.fh.Close()
}

func (b *BSONFile) Read(buf []byte) (int, error) {
	var err error
	buf, err = b.next()
	if err != nil {
		return 0, err
	}
	return len(buf), nil
}

func (b *BSONFile) ReadNext() ([]byte, error) {
	return b.next()
}

func (b *BSONFile) UnmarshalNext(dest interface{}) error {
	bytesRead, err := b.next()
	if err != nil {
		return err
	}
	return bson.Unmarshal(bytesRead, dest)
}

// next returns the next document in the bson file.
// This function is a modified version of the LoadNext function in:
// github.com/mongodb/mongo-tools/common/db/bson_stream.go
func (b *BSONFile) next() ([]byte, error) {
	var into []byte
	into = make([]byte, 4)
	// read the bson object size (a 4 byte integer)
	_, err := io.ReadAtLeast(b.fh, into[0:4], 4)
	if err != nil {
		return nil, err
	}

	bsonSize := int32((uint32(into[0]) << 0) | (uint32(into[1]) << 8) | (uint32(into[2]) << 16) | (uint32(into[3]) << 24))

	// Verify that we have a valid BSON document size.
	if bsonSize > MaxBSONSize || bsonSize < 5 {
		return nil, fmt.Errorf("Invalid bson size %d", bsonSize)
	}

	bigInto := make([]byte, bsonSize)
	copy(bigInto, into)
	_, err = io.ReadAtLeast(b.fh, bigInto[4:], int(bsonSize-4))
	if err != nil {
		return nil, err
	}

	return bigInto, nil
}
