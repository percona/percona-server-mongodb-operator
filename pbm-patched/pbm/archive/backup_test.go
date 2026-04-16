package archive

import (
	"context"
	"log"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var mClient *mongo.Client

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
	mClient, err = mongo.Connect(ctx, options.Client().ApplyURI(connStr))
	if err != nil {
		log.Fatalf("mongo client connect error: %v", err)
	}

	code := m.Run()

	err = mClient.Disconnect(ctx)
	if err != nil {
		log.Fatalf("mongo client disconnect error: %v", err)
	}
	if err := testcontainers.TerminateContainer(mongodbContainer); err != nil {
		log.Fatalf("failed to terminate container: %s", err)
	}

	os.Exit(code)
}

func TestListIndexes(t *testing.T) {
	db, coll := "testdb", "testcoll"
	ctx := context.Background()

	_, err := mClient.Database(db).Collection(coll).InsertOne(ctx, bson.M{
		"num":   101,
		"email": "abc@def.com",
	})
	if err != nil {
		t.Fatalf("creating test db: %v", err)
	}
	defer func() { _ = mClient.Database(db).Drop(ctx) }()

	bcp, err := NewBackup(ctx, BackupOptions{
		Client: mClient,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("single field index", func(t *testing.T) {
		coll := "simple"
		_, err := mClient.Database(db).Collection(coll).InsertOne(ctx, bson.M{
			"num":   101,
			"email": "abc@def.com",
		})
		if err != nil {
			t.Fatalf("creating test collection %s: %v", coll, err)
		}

		expectedKey := bson.D{{"num", int32(1)}}
		idxName, err := mClient.Database(db).Collection(coll).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: expectedKey,
		})
		if err != nil {
			t.Fatal(err)
		}

		idxs, err := bcp.listIndexes(ctx, db, coll)
		if err != nil {
			t.Fatalf("error within listIndexes: %v", err)
		}

		assertIdxKey(t, idxs, idxName, expectedKey)
	})

	t.Run("compound index", func(t *testing.T) {
		coll := "compound"

		_, err := mClient.Database(db).Collection(coll).InsertOne(ctx, bson.M{
			"num":   101,
			"email": "abc@def.com",
			"name":  "test",
		})
		if err != nil {
			t.Fatalf("creating test collection %s: %v", coll, err)
		}

		expectedKey := bson.D{{"num", int32(1)}, {"email", int32(-1)}}
		idxName, err := mClient.Database(db).Collection(coll).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: expectedKey,
		})
		if err != nil {
			t.Fatal(err)
		}

		idxs, err := bcp.listIndexes(ctx, db, coll)
		if err != nil {
			t.Fatalf("error within listIndexes: %v", err)
		}

		assertIdxKey(t, idxs, idxName, expectedKey)
	})

	t.Run("geospatial index", func(t *testing.T) {
		coll := "geospatial"

		_, err := mClient.Database(db).Collection(coll).InsertOne(ctx, bson.M{
			"num": 101,
			"location": bson.M{
				"type":        "Point",
				"coordinates": []float64{-73.856077, 40.848447},
			},
		})
		if err != nil {
			t.Fatalf("creating test collection %s: %v", coll, err)
		}

		expectedKey := bson.D{{"location", "2dsphere"}}
		idxName, err := mClient.Database(db).Collection(coll).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: expectedKey,
		})
		if err != nil {
			t.Fatal(err)
		}

		idxs, err := bcp.listIndexes(ctx, db, coll)
		if err != nil {
			t.Fatalf("error within listIndexes: %v", err)
		}

		assertIdxKey(t, idxs, idxName, expectedKey)
		assertIdxOpt(t, idxs, idxName, "2dsphereIndexVersion", int32(3), "{Key:2dsphereIndexVersion Value:3}")
	})

	t.Run("text index", func(t *testing.T) {
		coll := "text"

		_, err := mClient.Database(db).Collection(coll).InsertOne(ctx, bson.M{
			"num":  101,
			"desc": "Some test document for text search",
		})
		if err != nil {
			t.Fatalf("creating test collection %s: %v", coll, err)
		}

		expectedKeyRaw := bson.D{{"_fts", "text"}, {"_ftsx", int32(1)}}
		idxName, err := mClient.Database(db).Collection(coll).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: bson.D{{"desc", "text"}},
		})
		if err != nil {
			t.Fatal(err)
		}

		idxs, err := bcp.listIndexes(ctx, db, coll)
		if err != nil {
			t.Fatalf("error within listIndexes: %v", err)
		}

		assertIdxKey(t, idxs, idxName, expectedKeyRaw)
		assertIdxOpt(t, idxs, idxName, "weights", bson.D{{"desc", int32(1)}}, "{Key:weights Value:[{Key:desc Value:1}]}")
		assertIdxOpt(t, idxs, idxName, "default_language", "english", "{Key:default_language Value:english}")
		assertIdxOpt(t, idxs, idxName, "language_override", "language", "{Key:language_override Value:language}")
	})

	t.Run("hashed index", func(t *testing.T) {
		coll := "hashed"

		_, err := mClient.Database(db).Collection(coll).InsertOne(ctx, bson.M{
			"num":   101,
			"email": "abc@def.com",
		})
		if err != nil {
			t.Fatalf("creating test collection %s: %v", coll, err)
		}

		expectedKey := bson.D{{"email", "hashed"}}
		idxName, err := mClient.Database(db).Collection(coll).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: expectedKey,
		})
		if err != nil {
			t.Fatal(err)
		}

		idxs, err := bcp.listIndexes(ctx, db, coll)
		if err != nil {
			t.Fatalf("error within listIndexes: %v", err)
		}

		assertIdxKey(t, idxs, idxName, expectedKey)
	})

	t.Run("sparse index", func(t *testing.T) {
		coll := "sparse"

		_, err := mClient.Database(db).Collection(coll).InsertOne(ctx, bson.M{
			"num":            101,
			"email":          "abc@def.com",
			"optional_field": "test",
		})
		if err != nil {
			t.Fatalf("creating test collection %s: %v", coll, err)
		}

		expectedKey := bson.D{{"optional_field", int32(1)}}
		idxName, err := mClient.Database(db).Collection(coll).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys:    expectedKey,
			Options: options.Index().SetSparse(true),
		})
		if err != nil {
			t.Fatal(err)
		}

		idxs, err := bcp.listIndexes(ctx, db, coll)
		if err != nil {
			t.Fatalf("error within listIndexes: %v", err)
		}

		assertIdxKey(t, idxs, idxName, expectedKey)
		assertIdxOpt(t, idxs, idxName, "sparse", true, "{Key:sparse Value:true}")
	})

	t.Run("partial index", func(t *testing.T) {
		coll := "partial"

		_, err := mClient.Database(db).Collection(coll).InsertOne(ctx, bson.M{
			"num":    101,
			"email":  "abc@def.com",
			"status": "active",
		})
		if err != nil {
			t.Fatalf("creating test collection %s: %v", coll, err)
		}

		expectedKey := bson.D{{"email", int32(1)}}
		partialFilter := bson.D{{"status", "active"}}
		idxName, err := mClient.Database(db).Collection(coll).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys:    expectedKey,
			Options: options.Index().SetPartialFilterExpression(partialFilter),
		})
		if err != nil {
			t.Fatal(err)
		}

		idxs, err := bcp.listIndexes(ctx, db, coll)
		if err != nil {
			t.Fatalf("error within listIndexes: %v", err)
		}

		assertIdxKey(t, idxs, idxName, expectedKey)
		assertIdxOpt(t, idxs, idxName, "partialFilterExpression",
			partialFilter, "{Key:partialFilterExpression Value:[{Key:status Value:active}]}")
	})

	t.Run("ttl index", func(t *testing.T) {
		coll := "ttl"

		_, err := mClient.Database(db).Collection(coll).InsertOne(ctx, bson.M{
			"num":       101,
			"email":     "abc@def.com",
			"createdAt": time.Now(),
		})
		if err != nil {
			t.Fatalf("creating test collection %s: %v", coll, err)
		}

		expectedKey := bson.D{{"createdAt", int32(1)}}
		idxName, err := mClient.Database(db).Collection(coll).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys:    expectedKey,
			Options: options.Index().SetExpireAfterSeconds(60),
		})
		if err != nil {
			t.Fatal(err)
		}

		idxs, err := bcp.listIndexes(ctx, db, coll)
		if err != nil {
			t.Fatalf("error within listIndexes: %v", err)
		}

		assertIdxKey(t, idxs, idxName, expectedKey)
		assertIdxOpt(t, idxs, idxName, "expireAfterSeconds", int32(60), "{Key:expireAfterSeconds Value:60}")
	})

	t.Run("unique index", func(t *testing.T) {
		coll := "unique"

		_, err := mClient.Database(db).Collection(coll).InsertOne(ctx, bson.M{
			"num":   101,
			"email": "abc@def.com",
		})
		if err != nil {
			t.Fatalf("creating test collection %s: %v", coll, err)
		}

		expectedKey := bson.D{{"email", int32(1)}}
		idxName, err := mClient.Database(db).Collection(coll).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys:    expectedKey,
			Options: options.Index().SetUnique(true),
		})
		if err != nil {
			t.Fatal(err)
		}

		idxs, err := bcp.listIndexes(ctx, db, coll)
		if err != nil {
			t.Fatalf("error within listIndexes: %v", err)
		}

		assertIdxKey(t, idxs, idxName, expectedKey)
		assertIdxOpt(t, idxs, idxName, "unique", true, "{Key:unique Value:true}")
	})

	t.Run("only default", func(t *testing.T) {
		coll := "default"

		_, err := mClient.Database(db).Collection(coll).InsertOne(ctx, bson.M{
			"num":   101,
			"email": "abc@def.com",
		})
		if err != nil {
			t.Fatalf("creating test collection %s: %v", coll, err)
		}

		idxs, err := bcp.listIndexes(ctx, db, coll)
		if err != nil {
			t.Fatalf("error within listIndexes: %v", err)
		}

		if len(idxs) != 1 {
			t.Fatal("collection should have just default index")
		}
	})

	t.Run("few indexes on the same collection", func(t *testing.T) {
		coll := "more_than_one"

		_, err := mClient.Database(db).Collection(coll).InsertOne(ctx, bson.M{
			"num":   101,
			"email": "abc@def.com",
		})
		if err != nil {
			t.Fatalf("creating test collection %s: %v", coll, err)
		}

		expectedKey1 := bson.D{{"email", int32(1)}}
		idxName1, err := mClient.Database(db).Collection(coll).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys:    expectedKey1,
			Options: options.Index().SetUnique(true),
		})
		if err != nil {
			t.Fatal(err)
		}
		expectedKey2 := bson.D{{"num", int32(-1)}}
		idxName2, err := mClient.Database(db).Collection(coll).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: expectedKey2,
		})
		if err != nil {
			t.Fatal(err)
		}
		idxs, err := bcp.listIndexes(ctx, db, coll)
		if err != nil {
			t.Fatalf("error within listIndexes: %v", err)
		}

		if len(idxs) != 3 {
			t.Fatalf("expected 3 indexes, got=%d", len(idxs))
		}
		assertIdxKey(t, idxs, idxName1, expectedKey1)
		assertIdxOpt(t, idxs, idxName1, "unique", true, "{Key:unique Value:true}")
		assertIdxKey(t, idxs, idxName2, expectedKey2)
	})
}

func findIdx(idxs []IndexSpec, idxName string) (IndexSpec, bool) {
	var res IndexSpec
	for _, idx := range idxs {
		for _, idxProps := range idx {
			if idxProps.Key == "name" && idxProps.Value.(string) == idxName {
				return idx, true
			}
		}
	}
	return res, false
}

func findIdxKey(idx IndexSpec) (bson.D, bool) {
	for _, idxProps := range idx {
		if idxProps.Key == "key" {
			return idxProps.Value.(bson.D), true
		}
	}
	return bson.D{}, false
}

func assertIdxKey(t *testing.T, idxs []IndexSpec, idxName string, expectedKey bson.D) {
	t.Helper()
	idx, found := findIdx(idxs, idxName)
	if !found {
		t.Fatalf("cannot find index: %s", idxName)
	}
	idxKey, found := findIdxKey(idx)
	if !found {
		t.Fatalf("cannot find index key for index: %s", idxName)
	}
	if !reflect.DeepEqual(expectedKey, idxKey) {
		t.Fatalf("index key is wrong: want:%v, got:%v", expectedKey, idxKey)
	}
}

func assertIdxOpt(t *testing.T, idxs []IndexSpec, idxName string,
	optKey string, optVal any, optDesc string,
) {
	t.Helper()

	idx, found := findIdx(idxs, idxName)
	if !found {
		t.Fatalf("cannot find index: %s", idxName)
	}
	for _, idxProps := range idx {
		if idxProps.Key == optKey {
			if !reflect.DeepEqual(idxProps.Value, optVal) {
				t.Fatalf("wrong option for index: %+v, want option: %s", idx, optDesc)
			}
		}
	}
}
