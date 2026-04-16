package archive

import (
	"context"
	"encoding/hex"
	"hash/crc64"
	"io"
	"runtime"
	"slices"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/sync/errgroup"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

var RouterConfigCollections = []string{
	"chunks",
	"collections",
	"databases",
	"settings",
	"shards",
	"tags",
	"version",
}

const MetaFileV2 = "meta.pbm"

var validIndexOptions = map[string]bool{
	"2dsphereIndexVersion":    true,
	"background":              true,
	"bits":                    true,
	"bucketSize":              true,
	"coarsestIndexedLevel":    true,
	"collation":               true,
	"default_language":        true,
	"expireAfterSeconds":      true,
	"finestIndexedLevel":      true,
	"key":                     true,
	"language_override":       true,
	"max":                     true,
	"min":                     true,
	"name":                    true,
	"ns":                      true,
	"partialFilterExpression": true,
	"sparse":                  true,
	"storageEngine":           true,
	"textIndexVersion":        true,
	"unique":                  true,
	"v":                       true,
	"weights":                 true,
	"wildcardProjection":      true,
}

type ArchiveMetaV2 struct {
	Version       string `bson:"version"`
	ServerVersion string `bson:"serverVersion"`
	FCV           string `bson:"fcv"`

	Namespaces []*NamespaceV2 `bson:"specifications"`
}

type NamespaceV2 struct {
	DB      string `bson:"db"`
	Name    string `bson:"name"`
	Type    string `bson:"type"`
	UUID    string `bson:"uuid,omitempty"`
	Options bson.D `bson:"options,omitempty"`

	Indexes []IndexSpec `bson:"indexes"`

	CRC  int64 `bson:"crc"`
	Size int64 `bson:"size"`
}

func (s *NamespaceV2) NS() string {
	return s.DB + "." + strings.TrimPrefix(s.Name, "system.buckets.")
}

func (s *NamespaceV2) IsCollection() bool { return s.Type == "collection" }
func (s *NamespaceV2) IsView() bool       { return s.Type == "view" }
func (s *NamespaceV2) IsTimeseries() bool { return s.Type == "timeseries" }

func (s *NamespaceV2) IsSystemCollection() bool { return strings.HasPrefix(s.Name, "system.") }
func (s *NamespaceV2) IsBucketCollection() bool { return strings.HasPrefix(s.Name, "system.buckets") }

type IndexSpec bson.D

type BackupOptions struct {
	Client  *mongo.Client
	NewFile NewWriter

	NSFilter  NSFilterFn
	DocFilter DocFilterFn

	ParallelColls int
}

type backupImpl struct {
	conn    *mongo.Client
	newFile NewWriter

	nsFilter  NSFilterFn
	docFilter DocFilterFn

	serverVersion string
	fcv           string

	concurrency int
}

func NewBackup(ctx context.Context, options BackupOptions) (*backupImpl, error) {
	bcp := &backupImpl{
		conn:        options.Client,
		newFile:     options.NewFile,
		nsFilter:    DefaultNSFilter,
		docFilter:   DefaultDocFilter,
		concurrency: max(runtime.NumCPU()/2, 1),
	}

	if options.NSFilter != nil {
		bcp.nsFilter = options.NSFilter
	}
	if options.DocFilter != nil {
		bcp.docFilter = options.DocFilter
	}
	if options.ParallelColls > 0 {
		bcp.concurrency = options.ParallelColls
	}

	serverVersion, err := version.GetMongoVersion(ctx, bcp.conn)
	if err != nil {
		return nil, errors.Wrap(err, "get server version")
	}
	bcp.serverVersion = serverVersion.String()

	featureCompatibilityVersion, err := version.GetFCV(ctx, bcp.conn)
	if err != nil {
		return nil, errors.Wrap(err, "get featureCompatibilityVersion")
	}
	bcp.fcv = featureCompatibilityVersion

	return bcp, nil
}

func (bcp *backupImpl) Run(ctx context.Context) error {
	nss, err := bcp.listAllNamespaces(ctx)
	if err != nil {
		return errors.Wrap(err, "list namespaces")
	}

	err = bcp.dumpAllCollections(ctx, nss)
	if err != nil {
		return errors.Wrap(err, "dump namespaces")
	}

	slices.SortFunc(nss, func(a, b *NamespaceV2) int {
		return strings.Compare(a.NS(), b.NS())
	})

	err = bcp.writeMeta(nss)
	if err != nil {
		return errors.Wrap(err, "dump meta")
	}

	return nil
}

func (bcp *backupImpl) listAllNamespaces(ctx context.Context) ([]*NamespaceV2, error) {
	dbs, err := bcp.conn.ListDatabaseNames(ctx, bson.D{{"name", bson.M{"$ne": "local"}}})
	if err != nil {
		return nil, errors.Wrap(err, "list databases")
	}

	namespaces := []*NamespaceV2{}
	mu := &sync.Mutex{}

	eg, grpCtx := errgroup.WithContext(ctx)
	eg.SetLimit(bcp.concurrency)
	for _, db := range dbs {
		eg.Go(func() error {
			nss, err := bcp.listDBNamespaces(grpCtx, db)
			if err != nil {
				return errors.Wrapf(err, "list namespaces for %s", db)
			}

			mu.Lock()
			namespaces = append(namespaces, nss...)
			mu.Unlock()
			return nil
		})
	}

	err = eg.Wait()
	return namespaces, err
}

func (bcp *backupImpl) listDBNamespaces(ctx context.Context, db string) ([]*NamespaceV2, error) {
	filter := bson.D{}
	switch db {
	case "admin":
		filter = bson.D{{"name", bson.M{"$ne": "system.keys"}}}
	case "config":
		filter = bson.D{{"name", bson.M{"$in": RouterConfigCollections}}}
	}

	cur, err := bcp.conn.Database(db).ListCollections(ctx, filter)
	if err != nil {
		return nil, errors.Wrap(err, "list collections")
	}
	defer cur.Close(ctx)

	rv := []*NamespaceV2{}
	for cur.Next(ctx) {
		ns := &NamespaceV2{}
		err = cur.Decode(ns)
		if err != nil {
			return nil, errors.Wrap(err, "decode")
		}

		if !bcp.nsFilter(db + "." + ns.Name) {
			continue
		}

		// allow following namespaces only:
		// - admin.*
		// - config.(chunks|collections|databases|settings|shards|tags|version)
		// - <database>.buckets.*
		// - <database>.system.js
		// but not system collections like <database>.system.views
		if db != "admin" && db != "config" &&
			ns.IsSystemCollection() && !ns.IsBucketCollection() &&
			ns.Name != "system.js" {
			continue
		}

		ns.DB = db

		if _, data, ok := cur.Current.Lookup("info", "uuid").BinaryOK(); ok {
			ns.UUID = hex.EncodeToString(data)
		}

		if ns.Type != "view" {
			ns.Indexes, err = bcp.listIndexes(ctx, db, ns.Name)
			if err != nil {
				return nil, errors.Wrapf(err, "list indexes for %s", ns.Name)
			}
		} else {
			ns.Indexes = []IndexSpec{}
		}

		rv = append(rv, ns)
	}

	return rv, errors.Wrap(cur.Err(), "cursor")
}

// listIndexes fetches index definitions for the specified namespace.
// It returns dynamic bson.D index representation which is filtered by allowed index
// specification keys.
func (bcp *backupImpl) listIndexes(ctx context.Context, db, coll string) ([]IndexSpec, error) {
	cur, err := bcp.conn.Database(db).Collection(coll).Indexes().List(ctx)
	if err != nil {
		return nil, errors.Wrapf(err, "listIndexes cmd for ns: %s.%s", db, coll)
	}

	idxs := []bson.D{}
	err = cur.All(ctx, &idxs)
	if err != nil {
		return nil, errors.Wrapf(err, "decode indexes for ns: %s.%s", db, coll)
	}

	var idxSpecs []IndexSpec
	for i := range idxs {
		var idxSpec IndexSpec
		for _, opt := range idxs[i] {
			if _, ok := validIndexOptions[opt.Key]; ok {
				idxSpec = append(idxSpec, opt)
			}
		}
		idxSpecs = append(idxSpecs, idxSpec)
	}

	return idxSpecs, nil
}

func (bcp *backupImpl) dumpAllCollections(ctx context.Context, nss []*NamespaceV2) error {
	l := log.LogEventFromContext(ctx)
	eg, grpCtx := errgroup.WithContext(ctx)
	eg.SetLimit(bcp.concurrency)

	for _, ns := range nss {
		if ns.IsCollection() {
			eg.Go(func() error {
				err := bcp.dumpCollection(grpCtx, ns)
				if err != nil {
					return errors.Wrap(err, ns.NS())
				}

				l.Info("dump collection %q done (size: %d)", ns.NS(), ns.Size)
				return nil
			})
		}
	}

	return eg.Wait()
}

func (bcp *backupImpl) dumpCollection(ctx context.Context, ns *NamespaceV2) error {
	count, err := bcp.conn.Database(ns.DB).Collection(ns.Name).EstimatedDocumentCount(ctx)
	if err != nil {
		return errors.Wrap(err, "estimate document count")
	}
	if count == 0 {
		return nil
	}

	file, err := bcp.newFile(ns.NS())
	if err != nil {
		return errors.Wrap(err, "new file")
	}
	defer file.Close()

	cur, err := bcp.conn.Database(ns.DB).Collection(ns.Name).Find(ctx, bson.D{})
	if err != nil {
		return errors.Wrap(err, "find")
	}
	defer cur.Close(ctx)

	crc := crc64.New(crc64.MakeTable(crc64.ECMA))
	size := int64(0)
	for cur.Next(ctx) {
		if !bcp.docFilter(ns.NS(), cur.Current) {
			continue
		}

		crc.Write(cur.Current)

		n, err := file.Write(cur.Current)
		if err != nil {
			return errors.Wrap(err, "write")
		}
		if n != len(cur.Current) {
			return io.ErrShortWrite
		}
		size += int64(n)
	}

	err = cur.Err()
	if err != nil {
		var cmd mongo.CommandError
		if !errors.As(err, &cmd) || !cmd.HasErrorMessage("collection dropped") {
			return errors.Wrap(err, "cursor")
		}
	}

	err = file.Close()
	if err != nil {
		return errors.Wrap(err, "close")
	}

	ns.Size = size
	ns.CRC = int64(crc.Sum64())
	return nil
}

func (bcp *backupImpl) writeMeta(collections []*NamespaceV2) error {
	meta := &ArchiveMetaV2{
		Version:       version.Current().Version,
		ServerVersion: bcp.serverVersion,
		FCV:           bcp.fcv,
		Namespaces:    collections,
	}

	data, err := bson.Marshal(meta)
	if err != nil {
		return errors.Wrap(err, "marshal")
	}

	file, err := bcp.newFile(MetaFileV2)
	if err != nil {
		return errors.Wrap(err, "new file")
	}
	defer func() {
		err1 := errors.Wrap(file.Close(), "close")
		err = errors.Join(err, err1)
	}()

	n, err := file.Write(data)
	if err != nil {
		return errors.Wrap(err, "write")
	}
	if n != len(data) {
		return io.ErrShortWrite
	}

	return nil
}
