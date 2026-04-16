package restore

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	pbmlog "github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"
)

var leadConn connect.Client

const configSystemSessionsUUID = "92e8635902a743a09cf410d4a2a4a576"

func TestMain(m *testing.M) {
	ctx := context.Background()
	mongodbContainer, err := mongodb.Run(ctx, "perconalab/percona-server-mongodb:8.0.4-multi")
	if err != nil {
		log.Fatalf("error while creating mongo test container: %v", err)
	}
	connStr, err := mongodbContainer.ConnectionString(ctx)
	if err != nil {
		log.Fatalf("conn string error: %v", err)
	}

	leadConn, err = connect.Connect(ctx, connStr, "restore-test")
	if err != nil {
		log.Fatalf("mongo client connect error: %v", err)
	}

	code := m.Run()

	err = leadConn.Disconnect(ctx)
	if err != nil {
		log.Fatalf("mongo client disconnect error: %v", err)
	}
	if err := testcontainers.TerminateContainer(mongodbContainer); err != nil {
		log.Fatalf("failed to terminate container: %s", err)
	}

	os.Exit(code)
}

func TestFullRestoreConfigDatabases(t *testing.T) {
	ctx := context.Background()

	t.Run("restore empty dump and delete old data", func(t *testing.T) {
		var wantDocs int64 = 0
		r, backupMeta, remap := newConfigDatabasesTestObj(t, wantDocs)

		_, err := leadConn.ConfigDatabase().Collection("databases").InsertOne(ctx, bson.D{})
		if err != nil {
			t.Fatalf("preparing config.databases test docs: %v", err)
		}

		err = r.fullRestoreConfigDatabases(ctx, backupMeta, remap, remap)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}

		gotDocs := getNumOfDocsInDB(t, "databases")
		if gotDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of docs", wantDocs, gotDocs)
		}
	})

	t.Run("restore single document", func(t *testing.T) {
		var wantDocs int64 = 1
		r, backupMeta, remap := newConfigDatabasesTestObj(t, wantDocs)

		err := r.fullRestoreConfigDatabases(ctx, backupMeta, remap, remap)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}

		gotDocs := getNumOfDocsInDB(t, "databases")
		if gotDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of docs", wantDocs, gotDocs)
		}
	})

	t.Run("restore multiple documents with rs remapping", func(t *testing.T) {
		var wantDocs int64 = 10
		r, backupMeta, remap := newConfigDatabasesTestObj(t, wantDocs)
		sMap := util.MakeRSMapFunc(map[string]string{"rs1": "rsX"})

		err := r.fullRestoreConfigDatabases(ctx, backupMeta, remap, sMap)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}

		gotDocs := getNumOfDocsInDB(t, "databases")
		if gotDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of docs", wantDocs, gotDocs)
		}
		gotRemappedDocs := getNumOfMappedDatabasesDocs(t)
		if gotRemappedDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of remapped docs", wantDocs, gotRemappedDocs)
		}
	})

	t.Run("restore many documents", func(t *testing.T) {
		var wantDocs int64 = 10000
		r, backupMeta, remap := newConfigDatabasesTestObj(t, wantDocs)

		err := r.fullRestoreConfigDatabases(ctx, backupMeta, remap, remap)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}

		gotDocs := getNumOfDocsInDB(t, "databases")
		if gotDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of docs", wantDocs, gotDocs)
		}
	})
}

func TestFullRestoreConfigCollections(t *testing.T) {
	ctx := context.Background()

	t.Run("restore empty dump and delete old data", func(t *testing.T) {
		var wantDocs int64 = 0
		r, backupMeta, remap := newConfigCollectionsTestObj(t, wantDocs, false)

		_, err := leadConn.ConfigDatabase().Collection("collections").InsertOne(ctx, bson.D{})
		if err != nil {
			t.Fatalf("preparing config.collections test docs: %v", err)
		}

		_, err = r.fullRestoreConfigCollections(ctx, backupMeta, remap)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}

		gotDocs := getNumOfDocsInDB(t, "collections")
		if gotDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of docs", wantDocs, gotDocs)
		}
	})

	t.Run("restore single document", func(t *testing.T) {
		var wantDocs int64 = 1
		r, backupMeta, remap := newConfigCollectionsTestObj(t, wantDocs, false)

		gotUUID, err := r.fullRestoreConfigCollections(ctx, backupMeta, remap)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}

		if gotUUID != "" {
			t.Fatalf("want empty UUID, got %s", gotUUID)
		}

		gotDocs := getNumOfDocsInDB(t, "collections")
		if gotDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of docs", wantDocs, gotDocs)
		}
	})

	t.Run("restore documents and filter out system.sessions", func(t *testing.T) {
		var wantDocs int64 = 8
		r, backupMeta, remap := newConfigCollectionsTestObj(t, wantDocs+1, true)

		gotUUID, err := r.fullRestoreConfigCollections(ctx, backupMeta, remap)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}

		if gotUUID == "" {
			t.Fatalf("want non-empty UUID, got empty")
		}
		if gotUUID != configSystemSessionsUUID {
			t.Fatalf("wrong uuid for config.system.sessions, want=%s, got=%s",
				configSystemSessionsUUID, gotUUID)
		}

		gotDocs := getNumOfDocsInDB(t, "collections")
		if gotDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of docs", wantDocs, gotDocs)
		}
	})

	t.Run("restore many documents and filter out system.sessions", func(t *testing.T) {
		var wantDocs int64 = 10000
		r, backupMeta, remap := newConfigCollectionsTestObj(t, wantDocs+1, true)

		gotUUID, err := r.fullRestoreConfigCollections(ctx, backupMeta, remap)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}

		if gotUUID == "" {
			t.Fatalf("want non-empty UUID, got empty")
		}

		gotDocs := getNumOfDocsInDB(t, "collections")
		if gotDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of docs", wantDocs, gotDocs)
		}
	})
}

func TestFullRestoreConfigChunks(t *testing.T) {
	ctx := context.Background()

	t.Run("restore empty dump and delete old data", func(t *testing.T) {
		var wantDocs int64 = 0
		r, backupMeta, remap := newConfigChunksTestObj(t, wantDocs, false, "")

		_, err := leadConn.ConfigDatabase().Collection("chunks").InsertOne(ctx, bson.D{})
		if err != nil {
			t.Fatalf("preparing config.chunks test docs: %v", err)
		}

		err = r.fullRestoreConfigChunks(ctx, backupMeta, "", remap, remap)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}

		gotDocs := getNumOfDocsInDB(t, "chunks")
		if gotDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of docs", wantDocs, gotDocs)
		}
	})

	t.Run("restore single document", func(t *testing.T) {
		var wantDocs int64 = 1
		r, backupMeta, remap := newConfigChunksTestObj(t, wantDocs, false, "")

		err := r.fullRestoreConfigChunks(ctx, backupMeta, "", remap, remap)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}

		gotDocs := getNumOfDocsInDB(t, "chunks")
		if gotDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of docs", wantDocs, gotDocs)
		}
	})

	t.Run("restore documents and filter out system.sessions chunks", func(t *testing.T) {
		var wantDocs int64 = 15
		r, backupMeta, remap := newConfigChunksTestObj(t, wantDocs+1, true, configSystemSessionsUUID)

		err := r.fullRestoreConfigChunks(ctx, backupMeta, configSystemSessionsUUID, remap, remap)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}

		gotDocs := getNumOfDocsInDB(t, "chunks")
		if gotDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of docs", wantDocs, gotDocs)
		}
	})

	t.Run("restore multiple documents with shard remapping", func(t *testing.T) {
		var wantDocs int64 = 10
		r, backupMeta, remap := newConfigChunksTestObj(t, wantDocs, false, "")
		sMap := util.MakeRSMapFunc(map[string]string{"rs1": "rsX"})

		err := r.fullRestoreConfigChunks(ctx, backupMeta, "", remap, sMap)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}

		gotDocs := getNumOfDocsInDB(t, "chunks")
		if gotDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of docs", wantDocs, gotDocs)
		}
		gotRemappedDocs := getNumOfMappedChunksDocs(t)
		if gotRemappedDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of remapped docs", wantDocs, gotRemappedDocs)
		}
	})

	t.Run("restore many documents", func(t *testing.T) {
		var wantDocs int64 = 10500
		r, backupMeta, remap := newConfigChunksTestObj(t, wantDocs, false, "")

		err := r.fullRestoreConfigChunks(ctx, backupMeta, "", remap, remap)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}

		gotDocs := getNumOfDocsInDB(t, "chunks")
		if gotDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of docs", wantDocs, gotDocs)
		}
	})

	t.Run("restore many documents and filter out system.sessions chunks", func(t *testing.T) {
		var wantDocs int64 = 9500
		r, backupMeta, remap := newConfigChunksTestObj(t, wantDocs+1, true, configSystemSessionsUUID)

		err := r.fullRestoreConfigChunks(ctx, backupMeta, configSystemSessionsUUID, remap, remap)
		if err != nil {
			t.Fatalf("restore: %v", err)
		}

		gotDocs := getNumOfDocsInDB(t, "chunks")
		if gotDocs != wantDocs {
			t.Fatalf("want=%d, got=%d number of docs", wantDocs, gotDocs)
		}
	})
}

func createConfigDatabasesDocs(t *testing.T, n int64) *bytes.Buffer {
	t.Helper()

	type version struct {
		UUID      primitive.Binary    `bson:"uuid"`
		Timestamp primitive.Timestamp `bson:"timestamp"`
		LastMod   int64               `bson:"lastMod,omitempty"`
	}
	type databasesSchema struct {
		ID      string  `bson:"_id"`
		Primary string  `bson:"primary"`
		Version version `bson:"version"`
	}

	b := &bytes.Buffer{}
	for i := range n {
		dbDoc := databasesSchema{
			ID:      fmt.Sprintf("%d", i),
			Primary: "rs1",
			Version: version{
				UUID:      primitive.Binary{Subtype: 0x04, Data: fmt.Appendf([]byte("abcd"), "%d", i)},
				Timestamp: primitive.Timestamp{T: uint32(i), I: 1},
				LastMod:   i,
			},
		}
		bsonDoc, err := bson.Marshal(dbDoc)
		if err != nil {
			t.Fatal("marshal to bson:", err)
		}
		_, err = b.Write(bsonDoc)
		if err != nil {
			t.Fatal("write to buffer", err)
		}
	}
	return b
}

func createConfigCollectionsDocs(t *testing.T, n int64, addSession bool) *bytes.Buffer {
	t.Helper()

	type collectionsSchema struct {
		ID                string              `bson:"_id"`
		LastmodEpoch      primitive.ObjectID  `bson:"lastmodEpoch"`
		LastMod           primitive.DateTime  `bson:"lastMod"`
		Timestamp         primitive.Timestamp `bson:"timestamp"`
		UUID              primitive.Binary    `bson:"uuid,omitempty"`
		Key               bson.D              `bson:"key"`
		Unique            bool                `bson:"unique"`
		NoBalance         bool                `bson:"noBalance"`
		MaxChunkSizeBytes int64               `bson:"maxChunkSizeBytes"`
	}

	b := &bytes.Buffer{}
	for i := range n {
		dbDoc := collectionsSchema{
			ID:                fmt.Sprintf("%d", i),
			LastmodEpoch:      primitive.NewObjectID(),
			LastMod:           10,
			Timestamp:         primitive.Timestamp{T: uint32(i), I: 1},
			UUID:              primitive.Binary{Subtype: 0x04, Data: fmt.Appendf([]byte("abcd"), "%d", i)},
			Key:               bson.D{{"_id", 1}},
			Unique:            false,
			NoBalance:         false,
			MaxChunkSizeBytes: 200000,
		}
		if addSession && i == 0 {
			dbDoc.ID = "config.system.sessions"
			uuid, _ := hex.DecodeString(configSystemSessionsUUID)
			dbDoc.UUID = primitive.Binary{Subtype: 0x04, Data: uuid}
		}
		bsonDoc, err := bson.Marshal(dbDoc)
		if err != nil {
			t.Fatal("marshal to bson:", err)
		}
		_, err = b.Write(bsonDoc)
		if err != nil {
			t.Fatal("write to buffer", err)
		}
	}
	return b
}

func createConfigChunksDocs(t *testing.T, n int64, addSessionChunk bool, sysSessUUID string) *bytes.Buffer {
	t.Helper()

	type historyEntry struct {
		ValidAfter primitive.Timestamp `bson:"validAfter"`
		Shard      string              `bson:"shard"`
	}
	type chunksSchema struct {
		ID                  primitive.ObjectID  `bson:"_id"`
		UUID                primitive.Binary    `bson:"uuid"`
		Min                 bson.D              `bson:"min"`
		Max                 bson.D              `bson:"max"`
		Shard               string              `bson:"shard"`
		Lastmod             primitive.Timestamp `bson:"lastmod"`
		OnCurrentShardSince primitive.Timestamp `bson:"onCurrentShardSince"`
		History             []historyEntry      `bson:"history"`
	}

	b := &bytes.Buffer{}
	for i := range n {
		uuid := primitive.Binary{Subtype: 0x04, Data: fmt.Appendf([]byte("abcd"), "%d", i)}
		if addSessionChunk && i == 0 {
			uuidBytes, _ := hex.DecodeString(sysSessUUID)
			uuid = primitive.Binary{Subtype: 0x04, Data: uuidBytes}
		}

		dbDoc := chunksSchema{
			ID:                  primitive.NewObjectID(),
			UUID:                uuid,
			Min:                 bson.D{{"_id", primitive.MinKey{}}},
			Max:                 bson.D{{"_id", primitive.MaxKey{}}},
			Shard:               "rs1",
			Lastmod:             primitive.Timestamp{T: uint32(i), I: 0},
			OnCurrentShardSince: primitive.Timestamp{T: uint32(time.Now().Unix()), I: 15},
			History: []historyEntry{
				{
					ValidAfter: primitive.Timestamp{T: uint32(time.Now().Unix()), I: 15},
					Shard:      "rs1",
				},
			},
		}
		bsonDoc, err := bson.Marshal(dbDoc)
		if err != nil {
			t.Fatal("marshal to bson:", err)
		}
		_, err = b.Write(bsonDoc)
		if err != nil {
			t.Fatal("write to buffer", err)
		}
	}
	return b
}

func getNumOfDocsInDB(t *testing.T, coll string) int64 {
	t.Helper()

	n, err := leadConn.ConfigDatabase().
		Collection(coll).
		CountDocuments(context.Background(), bson.D{})
	if err != nil {
		t.Fatalf("counting docs: %v", err)
	}
	return n
}

func getNumOfMappedDatabasesDocs(t *testing.T) int64 {
	t.Helper()

	n, err := leadConn.ConfigDatabase().Collection("databases").
		CountDocuments(context.Background(), bson.D{{"primary", "rsX"}})
	if err != nil {
		t.Fatalf("counting db docs with remapping %v", err)
	}
	return n
}

func getNumOfMappedChunksDocs(t *testing.T) int64 {
	t.Helper()

	n, err := leadConn.ConfigDatabase().Collection("chunks").
		CountDocuments(context.Background(), bson.D{{"shard", "rsX"}})
	if err != nil {
		t.Fatalf("counting chunks docs with remapping %v", err)
	}
	return n
}

func newConfigDatabasesTestObj(t *testing.T, numOfDocs int64) (*Restore, *backup.BackupMeta, util.RSMapFunc) {
	r := New(leadConn, nil, topo.NodeBrief{}, nil, nil, 1, 1)
	r.log = pbmlog.DiscardEvent
	// create backup data on the storage
	r.bcpStg = newTestStorage(createConfigDatabasesDocs(t, numOfDocs))

	backupMeta := &backup.BackupMeta{
		Name:        "test",
		Compression: compress.CompressionTypeNone,
	}

	rsmap := util.MakeReverseRSMapFunc(map[string]string{})

	return r, backupMeta, rsmap
}

func newConfigCollectionsTestObj(
	t *testing.T,
	numOfDocs int64,
	addSession bool,
) (*Restore, *backup.BackupMeta, util.RSMapFunc) {
	r := New(leadConn, nil, topo.NodeBrief{}, nil, nil, 1, 1)
	r.log = pbmlog.DiscardEvent
	r.bcpStg = newTestStorage(createConfigCollectionsDocs(t, numOfDocs, addSession))

	backupMeta := &backup.BackupMeta{
		Name:        "test",
		Compression: compress.CompressionTypeNone,
	}

	rsmap := util.MakeReverseRSMapFunc(map[string]string{})

	return r, backupMeta, rsmap
}

func newConfigChunksTestObj(
	t *testing.T,
	numOfDocs int64,
	addSessionChunk bool,
	sysSessUUID string,
) (*Restore, *backup.BackupMeta, util.RSMapFunc) {
	r := New(leadConn, nil, topo.NodeBrief{}, nil, nil, 1, 1)
	r.log = pbmlog.DiscardEvent
	r.bcpStg = newTestStorage(createConfigChunksDocs(t, numOfDocs, addSessionChunk, sysSessUUID))

	backupMeta := &backup.BackupMeta{
		Name:        "test",
		Compression: compress.CompressionTypeNone,
	}

	rsmap := util.MakeReverseRSMapFunc(map[string]string{})

	return r, backupMeta, rsmap
}

var (
	errNotImp = errors.New("not implemented")

	_ storage.Storage = &testStorage{}
)

type testStorage struct {
	b *bytes.Buffer
}

func newTestStorage(data *bytes.Buffer) *testStorage {
	return &testStorage{b: data}
}

func (*testStorage) Type() storage.Type { return storage.Filesystem }

func (*testStorage) Save(_ string, _ io.Reader, _ ...storage.Option) error {
	return errNotImp
}

func (*testStorage) List(_, _ string) ([]storage.FileInfo, error) {
	return []storage.FileInfo{}, errNotImp
}

func (*testStorage) Delete(_ string) error { return errNotImp }

func (*testStorage) FileStat(_ string) (storage.FileInfo, error) {
	return storage.FileInfo{}, errNotImp
}

func (*testStorage) Copy(_, _ string) error { return errNotImp }

func (*testStorage) DownloadStat() storage.DownloadStat { return storage.DownloadStat{} }

func (s *testStorage) SourceReader(_ string) (io.ReadCloser, error) {
	return io.NopCloser(s.b), nil
}
