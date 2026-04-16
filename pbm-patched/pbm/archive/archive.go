package archive

import (
	"io"
	"strings"
	"sync"

	"github.com/mongodb/mongo-tools/common/archive"
	"github.com/mongodb/mongo-tools/common/db"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
	"golang.org/x/sync/errgroup"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

type (
	NewWriter func(ns string) (io.WriteCloser, error)
	NewReader func(ns string) (io.ReadCloser, error)
)

type archiveMeta struct {
	Header     *archive.Header `bson:",inline"`
	Namespaces []*Namespace    `bson:"namespaces"`
}

type Namespace struct {
	*archive.CollectionMetadata `bson:",inline"`

	CRC  int64 `bson:"crc"`
	Size int64 `bson:"size"`
}

const MetaFile = "metadata.json"

const MaxBSONSize = db.MaxBSONSize

var terminatorBytes = []byte{0xFF, 0xFF, 0xFF, 0xFF}

type (
	// NSFilterFn checks whether a namespace is selected for backup.
	// Useful when only some namespaces are selected for backup.
	NSFilterFn func(ns string) bool

	// DocFilter checks whether a document is selected for backup.
	// Useful when only some documents are selected for backup.
	DocFilterFn func(ns string, d bson.Raw) bool
)

func DefaultNSFilter(string) bool { return true }

func DefaultDocFilter(string, bson.Raw) bool { return true }

func Compose(w io.Writer, newReader NewReader, nsFilter NSFilterFn, concurrency int) error {
	meta, err := readMetadata(newReader)
	if err != nil {
		return errors.Wrap(err, "metadata")
	}

	if concurrency > 0 {
		// mongorestore uses this field as a number of
		// concurrent collections to restore at a moment
		meta.Header.ConcurrentCollections = int32(concurrency)
	}

	nss := make([]*Namespace, 0, len(meta.Namespaces))
	for _, ns := range meta.Namespaces {
		if nsFilter(NSify(ns.Database, ns.Collection)) {
			nss = append(nss, ns)
		}
	}

	meta.Namespaces = nss

	if err := writePrelude(w, meta); err != nil {
		return errors.Wrap(err, "prelude")
	}

	err = writeAllNamespaces(w, newReader,
		int(meta.Header.ConcurrentCollections),
		meta.Namespaces)
	return errors.Wrap(err, "write namespaces")
}

func writePrelude(w io.Writer, m *archiveMeta) error {
	prelude := archive.Prelude{Header: m.Header}
	prelude.NamespaceMetadatas = make([]*archive.CollectionMetadata, len(m.Namespaces))
	for i, n := range m.Namespaces {
		prelude.NamespaceMetadatas[i] = n.CollectionMetadata
	}

	err := prelude.Write(w)
	return errors.Wrap(err, "write")
}

func writeAllNamespaces(w io.Writer, newReader NewReader, lim int, nss []*Namespace) error {
	mu := sync.Mutex{}
	eg := errgroup.Group{}
	eg.SetLimit(lim)

	for _, ns := range nss {
		ns := ns

		eg.Go(func() error {
			if ns.Size == 0 {
				mu.Lock()
				defer mu.Unlock()

				return errors.Wrap(closeChunk(w, ns), "close empty chunk")
			}

			nss := NSify(ns.Database, ns.Collection)
			r, err := newReader(nss)
			if err != nil {
				return errors.Wrap(err, "new reader")
			}
			defer r.Close()

			err = splitChunks(r, MaxBSONSize*2, func(b []byte) error {
				mu.Lock()
				defer mu.Unlock()

				return errors.Wrap(writeChunk(w, ns, b), "write chunk")
			})
			if err != nil {
				return errors.Wrap(err, "split")
			}

			mu.Lock()
			defer mu.Unlock()

			return errors.Wrap(closeChunk(w, ns), "close chunk")
		})
	}

	return eg.Wait()
}

func splitChunks(r io.Reader, size int, write func([]byte) error) error {
	chunk := make([]byte, 0, size)
	buf, err := ReadBSONBuffer(r, nil)
	for err == nil {
		if len(chunk)+len(buf) > size {
			if err := write(chunk); err != nil {
				return err
			}
			chunk = chunk[:0]
		}

		chunk = append(chunk, buf...)
		buf, err = ReadBSONBuffer(r, buf[:cap(buf)])
	}

	if !errors.Is(err, io.EOF) {
		return errors.Wrap(err, "read bson")
	}

	if len(chunk) == 0 {
		return nil
	}

	return errors.Wrap(write(chunk), "last")
}

// ReadBSONBuffer reads raw bson document from r reader using buf buffer
func ReadBSONBuffer(r io.Reader, buf []byte) ([]byte, error) {
	var l [4]byte

	_, err := io.ReadFull(r, l[:])
	if err != nil {
		return nil, errors.Wrap(err, "length")
	}

	size := int(int32(l[0]) | int32(l[1])<<8 | int32(l[2])<<16 | int32(l[3])<<24)
	if size < 0 {
		return nil, bsoncore.ErrInvalidLength
	}

	if len(buf) < size {
		buf = make([]byte, size)
	}

	copy(buf, l[:])
	_, err = io.ReadFull(r, buf[4:size])
	if err != nil {
		return nil, err
	}

	if buf[size-1] != 0x00 {
		return nil, bsoncore.ErrMissingNull
	}

	return buf[:size], nil
}

func writeChunk(w io.Writer, ns *Namespace, data []byte) error {
	nsHeader := archive.NamespaceHeader{
		Database:   ns.Database,
		Collection: ns.Collection,
	}
	if ns.Type == "timeseries" {
		nsHeader.Collection = "system.buckets." + nsHeader.Collection
	}

	header, err := bson.Marshal(nsHeader)
	if err != nil {
		return errors.Wrap(err, "marshal")
	}

	if err := SecureWrite(w, header); err != nil {
		return errors.Wrap(err, "header")
	}

	if err := SecureWrite(w, data); err != nil {
		return errors.Wrap(err, "data")
	}

	err = SecureWrite(w, terminatorBytes)
	return errors.Wrap(err, "terminator")
}

func closeChunk(w io.Writer, ns *Namespace) error {
	nsHeader := archive.NamespaceHeader{
		Database:   ns.Database,
		Collection: ns.Collection,
		EOF:        true,
		CRC:        ns.CRC,
	}
	if ns.Type == "timeseries" {
		nsHeader.Collection = "system.buckets." + nsHeader.Collection
	}

	header, err := bson.Marshal(nsHeader)
	if err != nil {
		return errors.Wrap(err, "marshal")
	}

	if err := SecureWrite(w, header); err != nil {
		return errors.Wrap(err, "header")
	}

	err = SecureWrite(w, terminatorBytes)
	return errors.Wrap(err, "terminator")
}

func readMetadata(newReader NewReader) (*archiveMeta, error) {
	r, err := newReader(MetaFile)
	if err != nil {
		return nil, errors.Wrap(err, "new metafile reader")
	}
	defer r.Close()

	return ReadMetadata(r)
}

func ReadMetadata(r io.Reader) (*archiveMeta, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, errors.Wrap(err, "read")
	}

	meta := &archiveMeta{}
	err = bson.UnmarshalExtJSON(data, true, meta)
	return meta, errors.Wrap(err, "unmarshal")
}

func SecureWrite(w io.Writer, data []byte) error {
	n, err := w.Write(data)
	if err != nil {
		return errors.Wrap(err, "write")
	}
	if n != len(data) {
		return io.ErrShortWrite
	}

	return nil
}

func NSify(db, coll string) string {
	return db + "." + strings.TrimPrefix(coll, "system.buckets.")
}
