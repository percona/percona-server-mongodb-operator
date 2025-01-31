package archive

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"

	mtArchive "github.com/mongodb/mongo-tools/common/archive"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

func GenerateV1FromV2(ctx context.Context, stg storage.Storage, bcp, rs string) error {
	metaV2Filename := fmt.Sprintf("%s/%s/%s", bcp, rs, MetaFileV2)
	rdr, err := stg.SourceReader(metaV2Filename)
	if err != nil {
		return errors.Wrapf(err, "open file: %q", metaV2Filename)
	}
	defer func() {
		err := rdr.Close()
		if err != nil {
			log.LogEventFromContext(ctx).Error("close %q: %v", metaV2Filename, err)
		}
	}()

	data, err := io.ReadAll(rdr)
	if err != nil {
		return errors.Wrap(err, "read")
	}

	metaV2 := &ArchiveMetaV2{}
	err = bson.Unmarshal(data, &metaV2)
	if err != nil {
		return errors.Wrap(err, "unmarshal")
	}

	metaV1, err := convertMetaToV1(metaV2)
	if err != nil {
		return errors.Wrap(err, "convert")
	}
	data, err = bson.MarshalExtJSONIndent(metaV1, true, true, "", "  ")
	if err != nil {
		return errors.Wrap(err, "marshal")
	}

	metaV1Filename := fmt.Sprintf("%s/%s/%s", bcp, rs, MetaFile)
	err = stg.Save(metaV1Filename, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return errors.Wrapf(err, "save %q", metaV1Filename)
	}

	return nil
}

func convertMetaToV1(metaV2 *ArchiveMetaV2) (*archiveMeta, error) {
	metaV1 := &archiveMeta{
		Header: &mtArchive.Header{
			ConcurrentCollections: int32(runtime.NumCPU() / 2),

			FormatVersion: "0.1",
			ServerVersion: metaV2.ServerVersion,
			ToolVersion:   metaV2.Version,
		},
		Namespaces: []*Namespace{},
	}

	for _, coll := range metaV2.Namespaces {
		if coll.DB != "admin" && coll.DB != "config" &&
			coll.IsSystemCollection() && coll.Name != "system.js" {
			continue
		}

		ns, err := convertCollSpecToV1(coll)
		if err != nil {
			return nil, errors.Wrap(err, coll.NS())
		}

		if coll.IsTimeseries() {
			bucketName := "system.buckets." + coll.Name
			for _, coll := range metaV2.Namespaces {
				if coll.Name == bucketName {
					ns.CRC = coll.CRC
					ns.Size = coll.Size
					break
				}
			}
		}

		metaV1.Namespaces = append(metaV1.Namespaces, ns)
	}

	// the sorting is not important for use. just simplify testing
	slices.SortFunc(metaV1.Namespaces, func(a, b *Namespace) int {
		if x := strings.Compare(a.Database, b.Database); x != 0 {
			return x
		}
		return strings.Compare(a.Collection, b.Collection)
	})

	return metaV1, nil
}

func convertCollSpecToV1(coll *NamespaceV2) (*Namespace, error) {
	ns := &Namespace{
		CollectionMetadata: &mtArchive.CollectionMetadata{
			Database:   coll.DB,
			Collection: coll.Name,
			Size:       int(coll.Size),
			Type:       coll.Type,
		},
	}

	metadata := bson.D{}
	if coll.Options != nil {
		metadata = append(metadata, bson.E{"options", coll.Options})
	}
	metadata = append(metadata, bson.E{"indexes", coll.Indexes})
	if coll.UUID != "" {
		metadata = append(metadata, bson.E{"uuid", coll.UUID})
	}
	metadata = append(metadata, bson.E{"collectionName", coll.Name})
	metadata = append(metadata, bson.E{"type", coll.Type})

	data, err := bson.MarshalExtJSON(metadata, true, true)
	if err != nil {
		return nil, err
	}

	ns.Metadata = string(data)
	ns.CRC = coll.CRC
	ns.Size = coll.Size
	return ns, nil
}
