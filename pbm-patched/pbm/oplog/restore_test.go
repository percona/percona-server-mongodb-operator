package oplog

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mongodb/mongo-tools/common/db"
	"github.com/mongodb/mongo-tools/common/idx"
	"github.com/mongodb/mongo-tools/mongorestore/ns"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/percona/percona-backup-mongodb/pbm/snapshot"
)

func newOplogRestoreTest(mdb mDBCl) *OplogRestore {
	noUUID, _ := ns.NewMatcher(dontPreserveUUID)
	matcher, _ := ns.NewMatcher(append(snapshot.ExcludeFromRestore, excludeFromOplog...))
	return &OplogRestore{
		mdb:             mdb,
		ver:             &db.Version{7, 0, 0},
		excludeNS:       matcher,
		noUUIDns:        noUUID,
		preserveUUIDopt: true,
		preserveUUID:    true,
		indexCatalog:    idx.NewIndexCatalog(),
		filter:          DefaultOpFilter,
	}
}

type mdbTestClient struct {
	applyOpsInv []map[string]string
}

func newMDBTestClient() *mdbTestClient {
	return &mdbTestClient{applyOpsInv: []map[string]string{}}
}

func (d *mdbTestClient) getUUIDForNS(_ context.Context, _ string) (primitive.Binary, error) {
	return primitive.Binary{Subtype: 0x00, Data: []byte{0x01, 0x02, 0x03}}, nil
}

func (d *mdbTestClient) ensureCollExists(_ string) error {
	return nil
}

func (d *mdbTestClient) applyOps(entries []interface{}) error {
	if len(entries) != 1 {
		return errors.New("applyOps without single oplog entry")
	}

	oe := entries[0].(db.Oplog)
	invParams := map[string]string{
		"op": oe.Operation,
		"ns": oe.Namespace,
	}
	if oe.Operation == "c" && oe.Object != nil && len(oe.Object) > 0 {
		invParams["cmd"] = oe.Object[0].Key
		invParams["coll"] = oe.Object[0].Value.(string)
	}
	d.applyOpsInv = append(d.applyOpsInv, invParams)

	return nil
}

func TestIsOpForCloning(t *testing.T) {
	oRestore := newOplogRestoreTest(&mdbTestClient{})
	_ = oRestore.SetCloneNS(context.Background(), snapshot.CloneNS{FromNS: "mydb.cloningFrom", ToNS: "mydb.cloningTo"})

	testCases := []struct {
		desc         string
		entry        *db.Oplog
		isForCloning bool
	}{
		// i op
		{
			desc:         "insert op for cloning ",
			entry:        createInsertOp(t, "mydb.cloningFrom"),
			isForCloning: true,
		},
		{
			desc:         "insert op, collection not for cloning",
			entry:        createInsertOp(t, "mydb.x"),
			isForCloning: false,
		},
		{
			desc:         "insert op, db not for cloning",
			entry:        createInsertOp(t, "x.cloningFrom"),
			isForCloning: false,
		},

		// u op
		{
			desc:         "update op for cloning ",
			entry:        createUpdateOp(t, "mydb.cloningFrom"),
			isForCloning: true,
		},
		{
			desc:         "update op, collection not for cloning",
			entry:        createUpdateOp(t, "mydb.x"),
			isForCloning: false,
		},
		{
			desc:         "update op, db not for cloning",
			entry:        createUpdateOp(t, "x.cloningFrom"),
			isForCloning: false,
		},

		// d op
		{
			desc:         "delete op for cloning ",
			entry:        createDeleteOp(t, "mydb.cloningFrom"),
			isForCloning: true,
		},
		{
			desc:         "delete op, collection not for cloning",
			entry:        createDeleteOp(t, "mydb.x"),
			isForCloning: false,
		},
		{
			desc:         "delete op, db not for cloning",
			entry:        createDeleteOp(t, "x.cloningFrom"),
			isForCloning: false,
		},
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			res := oRestore.isOpForCloning(tC.entry)
			if res != tC.isForCloning {
				t.Errorf("%s: for entry: %+v isOpForCloning is: %t, but it should be opposite",
					tC.desc, tC.entry, tC.isForCloning)
			}
		})
	}
}

func TestIsOpAllowed(t *testing.T) {
	testCases := []struct {
		desc      string
		entry     *db.Oplog
		opAllowed bool
	}{
		{
			desc:      "any other than config",
			entry:     createInsertOp(t, "db1.c1"),
			opAllowed: true,
		},
		{
			desc:      "config.chunks",
			entry:     createInsertOp(t, "config.chunks"),
			opAllowed: true,
		},
		{
			desc:      "config.collections",
			entry:     createInsertOp(t, "config.collections"),
			opAllowed: true,
		},
		{
			desc:      "config.databases",
			entry:     createInsertOp(t, "config.databases"),
			opAllowed: true,
		},
		{
			desc:      "config.shards",
			entry:     createInsertOp(t, "config.shards"),
			opAllowed: true,
		},
		{
			desc:      "config.tags",
			entry:     createUpdateOp(t, "config.tags"),
			opAllowed: true,
		},
		{
			desc:      "config.version",
			entry:     createDeleteOp(t, "config.version"),
			opAllowed: true,
		},
		{
			desc:      "dissalowed config collection",
			entry:     createInsertOp(t, "config.mongos"),
			opAllowed: false,
		},
		{
			desc:      "wrong config.settings doc will not break",
			entry:     createDeleteOp(t, "config.settings"),
			opAllowed: true,
		},
		{
			desc:      "settings doc for balancer",
			entry:     configSettingsBalancerEntry(),
			opAllowed: false,
		},
		{
			desc:      "settings doc for automerge",
			entry:     configSettingsAutomergeEntry(),
			opAllowed: false,
		},
		{
			desc:      "allowed settings doc",
			entry:     configSettingsAnyOtherEntry(),
			opAllowed: true,
		},
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			res := isOpAllowed(tC.entry)
			if res != tC.opAllowed {
				t.Errorf("%s: for entry: %+v isOpAllowed is: %t, but it should be opposite",
					tC.desc, tC.entry, tC.opAllowed)
			}
		})
	}
}

func TestIsConfigCollectionsDocAllowed(t *testing.T) {
	tUUID := "06f587d338174590ad1cadd65bf9ee4f"
	tUUID2 := "06f587d338174590ad1cadd65bf9ee4a"
	testCases := []struct {
		desc         string
		entry        *db.Oplog
		sessUUID     string
		wantSessUUID string
		opAllowed    bool
	}{
		{
			desc:         "not config db entry, sess uuid specified",
			entry:        createInsertOp(t, "a.b"),
			sessUUID:     tUUID,
			wantSessUUID: tUUID,
			opAllowed:    true,
		},
		{
			desc:         "not config db entry, sess uuid unspecified",
			entry:        createInsertOp(t, "a.b"),
			sessUUID:     "",
			wantSessUUID: "",
			opAllowed:    true,
		},
		{
			desc:         "not config.collections entry",
			entry:        createConfigChunksEntry("abc"),
			sessUUID:     tUUID,
			wantSessUUID: tUUID,
			opAllowed:    true,
		},
		{
			desc:         "config.collections entry for sessions with empty uuid",
			entry:        createConfigCollectionsEntry("config.system.sessions", ""),
			sessUUID:     tUUID,
			wantSessUUID: tUUID,
			opAllowed:    false,
		},
		{
			desc:         "config.collections entry for sessions that should not be allowed",
			entry:        createConfigCollectionsEntry("config.system.sessions", tUUID),
			sessUUID:     tUUID,
			wantSessUUID: tUUID,
			opAllowed:    false,
		},
		{
			desc:         "config.collections entry for session that doesn't match uuid",
			entry:        createConfigCollectionsEntry("config.system.sessions", tUUID),
			sessUUID:     tUUID2,
			wantSessUUID: tUUID2,
			opAllowed:    false,
		},
		{
			desc:         "insert config.collections entry for session and sessUUID is not specified",
			entry:        createConfigCollectionsEntry("config.system.sessions", tUUID),
			sessUUID:     "",
			wantSessUUID: tUUID,
			opAllowed:    false,
		},
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			sessUUID := tC.sessUUID
			res := isConfigCollectionsDocAllowed(tC.entry, &sessUUID)

			if res != tC.opAllowed {
				t.Errorf("%s: for entry: %+v isConfigCollectionsDocAllowed is: %t, but it should be opposite",
					tC.desc, tC.entry, res)
			}

			if tC.wantSessUUID != sessUUID {
				t.Errorf("%s: wrong sessUUID, want=%s, got=%s",
					tC.desc, tC.wantSessUUID, sessUUID)
			}
		})
	}
}

func TestIsConfigChunksDocAllowed(t *testing.T) {
	testCases := []struct {
		desc      string
		entry     *db.Oplog
		uuid      string
		opAllowed bool
	}{
		{
			desc:      "not config db entry",
			entry:     createInsertOp(t, "a.b"),
			opAllowed: true,
		},
		{
			desc:      "not config.chunks entry",
			entry:     createConfigCollectionsEntry("abc", "06f587d338174590ad1cadd65bf9ee5a"),
			uuid:      "06f587d338174590ad1cadd65bf9ee5a",
			opAllowed: true,
		},
		{
			desc:      "empty uuid",
			entry:     createConfigChunksEntry(""),
			uuid:      "06f587d338174590ad1cadd65bf9ee4f",
			opAllowed: true,
		},
		{
			desc:      "config.chunks entry for sessions that should not be allowed",
			entry:     createConfigChunksEntry("06f587d338174590ad1cadd65bf9ee4f"),
			uuid:      "06f587d338174590ad1cadd65bf9ee4f",
			opAllowed: false,
		},
		{
			desc:      "config.chunks entry for session that doesn't match uuid",
			entry:     createConfigChunksEntry("06f587d338174590ad1cadd65bf9ee5a"),
			uuid:      "06f587d338174590ad1cadd65bf9ee4f",
			opAllowed: true,
		},
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			res := isConfigChunksDocAllowed(tC.entry, tC.uuid)
			if res != tC.opAllowed {
				t.Errorf("%s: for entry: %+v isConfigChunksDocAllowed is: %t, but it should be opposite",
					tC.desc, tC.entry, res)
			}
		})
	}
}

func TestIsRoutingDocExcluded(t *testing.T) {
	tUUID := "06f587d338174590ad1cadd65bf9ee4f"
	tUUID2 := "06f587d338174590ad1cadd65bf9ee4a"
	testCases := []struct {
		desc         string
		entry        *db.Oplog
		sessUUID     string
		wantSessUUID string
		excluded     bool
	}{
		{
			desc:         "different ns, op should be included",
			entry:        createInsertOp(t, "a.b"),
			sessUUID:     tUUID,
			wantSessUUID: tUUID,
			excluded:     false,
		},
		{
			desc:         "config.collections should be included, sessUUID specified",
			entry:        createConfigCollectionsEntry("a.b", tUUID),
			sessUUID:     tUUID2,
			wantSessUUID: tUUID2,
			excluded:     false,
		},
		{
			desc:         "config.collections should be included, sessUUID not specified",
			entry:        createConfigCollectionsEntry("a.b", tUUID),
			sessUUID:     "",
			wantSessUUID: "",
			excluded:     false,
		},
		{
			desc:         "config.collections should be excluded, sessUUID specified",
			entry:        createConfigCollectionsEntry("config.system.sessions", tUUID),
			sessUUID:     tUUID2,
			wantSessUUID: tUUID2,
			excluded:     true,
		},
		{
			desc:         "config.collections should be excluded, sessUUID not specified, sessUUID updated",
			entry:        createConfigCollectionsEntry("config.system.sessions", tUUID),
			sessUUID:     "",
			wantSessUUID: tUUID,
			excluded:     true,
		},
		{
			desc:         "config.chunks should be included",
			entry:        createConfigChunksEntry(tUUID),
			sessUUID:     tUUID2,
			wantSessUUID: tUUID2,
			excluded:     false,
		},
		{
			desc:         "config.chunks should be excluded",
			entry:        createConfigChunksEntry(tUUID),
			sessUUID:     tUUID,
			wantSessUUID: tUUID,
			excluded:     true,
		},
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			sessUUID := tC.sessUUID
			res := isRoutingDocExcluded(tC.entry, &sessUUID)

			if res != tC.excluded {
				t.Errorf("%s: isRoutingDocExcluded for entry: %+v  is: %t, but it should be opposite",
					tC.desc, tC.entry, res)
			}

			if tC.wantSessUUID != sessUUID {
				t.Errorf("%s: wrong sessUUID, want=%s, got=%s",
					tC.desc, tC.wantSessUUID, sessUUID)
			}
		})
	}
}

func TestApply(t *testing.T) {
	t.Run("collection restore", func(t *testing.T) {
		testCases := []struct {
			desc      string
			oplogFile string
			resOps    []string
			resNS     []string
			resCmd    []string
			resColl   []string
		}{
			{
				desc:      "collection: create-drop-create",
				oplogFile: "ops_cmd_create_drop",
				resOps:    []string{"c", "c", "c", "c", "c"},
				resNS:     []string{"mydb.$cmd", "mydb.$cmd", "mydb.$cmd", "mydb.$cmd", "mydb.$cmd"},
				resCmd:    []string{"drop", "create", "drop", "drop", "create"},
				resColl:   []string{"c1", "c1", "c1", "c2", "c2"},
			},
			// todo: add more cases
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				db := newMDBTestClient()
				oRestore := newOplogRestoreTest(db)

				fr := useTestFile(t, tC.oplogFile)

				_, err := oRestore.Apply(fr)
				if err != nil {
					t.Fatalf("error while applying oplog: %v", err)
				}

				if len(tC.resOps) != len(db.applyOpsInv) {
					t.Errorf("wrong number of applyOps invocation, want=%d, got=%d", len(tC.resOps), len(db.applyOpsInv))
				}
				for i, wantOp := range tC.resOps {
					gotOp := db.applyOpsInv[i]["op"]
					if wantOp != gotOp {
						t.Errorf("wrong #%d. operation: want=%s, got=%s", i, wantOp, gotOp)
					}
				}
				for i, wantNS := range tC.resNS {
					gotNS := db.applyOpsInv[i]["ns"]
					if wantNS != gotNS {
						t.Errorf("wrong #%d. namespace: want=%s, got=%s", i, wantNS, gotNS)
					}
				}
				for i, wantCmd := range tC.resCmd {
					gotCmd := db.applyOpsInv[i]["cmd"]
					if wantCmd != gotCmd {
						t.Errorf("wrong #%d. command: want=%s, got=%s", i, wantCmd, gotCmd)
					}
				}
				for i, wantColl := range tC.resColl {
					gotColl := db.applyOpsInv[i]["coll"]
					if wantColl != gotColl {
						t.Errorf("wrong #%d. collection: want=%s, got=%s", i, wantColl, gotColl)
					}
				}
			})
		}
	})

	t.Run("selective restore", func(t *testing.T) {
		// todo: add tests
	})

	t.Run("index restore", func(t *testing.T) {
		testCases := []struct {
			desc      string
			oplogFile string
			db        string
			coll      string
			idxs      bson.M
		}{
			{
				desc:      "index: dropIndexes-createIndexes",
				oplogFile: "ops_cmd_createIndexes_dropIndexes",
				db:        "mydb",
				coll:      "c1",
				idxs:      bson.M{"fieldX": -1, "fieldZ": -1},
			},
			// todo: add more cases
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				db := newMDBTestClient()
				oRestore := newOplogRestoreTest(db)

				fr := useTestFile(t, tC.oplogFile)

				_, err := oRestore.Apply(fr)
				if err != nil {
					t.Fatalf("error while applying oplog: %v", err)
				}

				idxDocs := oRestore.indexCatalog.GetIndexes(tC.db, tC.coll)
				if len(idxDocs) != len(tC.idxs) {
					t.Errorf("wrong number of indexes: want=%d, got=%d", len(tC.idxs), len(idxDocs))
				}
				for _, idxDoc := range idxDocs {
					if _, ok := tC.idxs[idxDoc.Key[0].Key]; !ok {
						t.Errorf("wrong key: %v", idxDoc.Key[0])
					}
				}
			})
		}
	})

	t.Run("cloning namespace", func(t *testing.T) {
		testCases := []struct {
			desc      string
			oplogFile string
			nsFrom    string
			nsTo      string
			resOps    []string
			resNS     []string
		}{
			{
				desc:      "clone: insert, update, delete ops",
				oplogFile: "ops_i_u_d",
				nsFrom:    "mydb.c1",
				nsTo:      "mydb.c1_clone",
				resOps:    []string{"i", "u", "d"},
				resNS:     []string{"mydb.c1_clone", "mydb.c1_clone", "mydb.c1_clone"},
			},
			{
				desc:      "ignore namespaces not relevant for cloning",
				oplogFile: "ops_i_u_d",
				nsFrom:    "mydb.xyz",
				nsTo:      "mydb.xyz_clone",
				resOps:    []string{},
				resNS:     []string{},
			},
			{
				desc:      "ignore noop op",
				oplogFile: "ops_n",
				nsFrom:    "mydb.xyz",
				nsTo:      "mydb.xyz_clone",
				resOps:    []string{},
				resNS:     []string{},
			},
			// add index creation
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				db := newMDBTestClient()
				oRestore := newOplogRestoreTest(db)
				_ = oRestore.SetCloneNS(context.Background(), snapshot.CloneNS{FromNS: tC.nsFrom, ToNS: tC.nsTo})

				fr := useTestFile(t, tC.oplogFile)

				_, err := oRestore.Apply(fr)
				if err != nil {
					t.Fatalf("error while applying oplog: %v", err)
				}

				if len(tC.resOps) != len(db.applyOpsInv) {
					t.Errorf("wrong number of applyOps invocation, want=%d, got=%d", len(tC.resOps), len(db.applyOpsInv))
				}
				for i, wantOp := range tC.resOps {
					gotOp := db.applyOpsInv[i]["op"]
					if wantOp != gotOp {
						t.Errorf("wrong #%d. operation: want=%s, got=%s", i, wantOp, gotOp)
					}
				}
				for i, wantNS := range tC.resNS {
					gotNS := db.applyOpsInv[i]["ns"]
					if wantNS != gotNS {
						t.Errorf("wrong #%d. namespace: want=%s, got=%s", i, wantNS, gotNS)
					}
				}
			})
		}
	})
}

func useTestFile(t *testing.T, testFileName string) io.ReadCloser {
	t.Helper()

	f := fmt.Sprintf("%s.json", testFileName)
	jsonData, err := os.ReadFile(filepath.Join("./testdata", f))
	if err != nil {
		t.Fatalf("failed to read test json file: filename=%s, err=%v", f, err)
	}

	var jsonDocs []db.Oplog
	err = bson.UnmarshalExtJSON(jsonData, false, &jsonDocs)
	if err != nil {
		t.Fatalf("failed to parse test json array: filename=%s, err=%v", f, err)
	}

	b := &bytes.Buffer{}
	for _, jsonDoc := range jsonDocs {
		bsonDoc, err := bson.Marshal(jsonDoc)
		if err != nil {
			t.Fatalf("failed to marshal json to bson: %v", err)
		}
		_, err = b.Write(bsonDoc)
		if err != nil {
			t.Fatalf("Failed to write BSON: %v", err)
		}
	}

	return io.NopCloser(b)
}

func createInsertOp(t *testing.T, ns string) *db.Oplog {
	t.Helper()
	iOpJSON := `
		{
		  "lsid": {
			"id": {
			  "$binary": {
				"base64": "YYbkO7kpRt6xFqJqIh+h9g==",
				"subType": "04"
			  }
			},
			"uid": {
			  "$binary": {
				"base64": "8L/kOoqHkvDRIRJTrmrrO3wwOr+ToO8WLvmn15Ql7G0=",
				"subType": "00"
			  }
			}
		  },
		  "txnNumber": {
			"$numberLong": "9"
		  },
		  "op": "i",
		  "ns": "db.coll",
		  "ui": {
			"$binary": {
			  "base64": "v+mHa8niRBKG7Z+uqJGARQ==",
			  "subType": "04"
			}
		  },
		  "o": {
			"_id": {
			  "$oid": "6747008178d82a2b1134a2b8"
			},
			"d": {
			  "$numberInt": "6"
			},
			"desc": "doc-6"
		  },
		  "o2": {
			"_id": {
			  "$oid": "6747008178d82a2b1134a2b8"
			}
		  },
		  "stmtId": {
			"$numberInt": "0"
		  },
		  "ts": {
			"$timestamp": {
			  "t": 1732706433,
			  "i": 1
			}
		  },
		  "t": {
			"$numberLong": "2"
		  },
		  "v": {
			"$numberLong": "2"
		  },
		  "wall": {
			"$date": {
			  "$numberLong": "1732706433987"
			}
		  },
		  "prevOpTime": {
			"ts": {
			  "$timestamp": {
				"t": 0,
				"i": 0
			  }
			},
			"t": {
			  "$numberLong": "-1"
			}
		  }
		}`

	return replaceNsWithinOpEntry(t, iOpJSON, ns)
}

func createInsertSimpleOp(t *testing.T, ns string) *db.Oplog {
	t.Helper()
	iOpJSON := `
		{
		  "op": "i",
		  "ns": "db.coll",
		  "o": {
			"_id": {
			  "$oid": "6747008178d82a2b1134a2b8"
			},
			"d": {
			  "$numberInt": "6"
			},
			"desc": "doc-6"
		  },
		  "o2": {
			"_id": {
			  "$oid": "6747008178d82a2b1134a2b8"
			}
		  },
		  "stmtId": {
			"$numberInt": "0"
		  },
		  "ts": {
			"$timestamp": {
			  "t": 1732706433,
			  "i": 1
			}
		  },
		  "t": {
			"$numberLong": "2"
		  },
		  "v": {
			"$numberLong": "2"
		  },
		  "wall": {
			"$date": {
			  "$numberLong": "1732706433987"
			}
		  },
		  "prevOpTime": {
			"ts": {
			  "$timestamp": {
				"t": 0,
				"i": 0
			  }
			},
			"t": {
			  "$numberLong": "-1"
			}
		  }
		}`

	return replaceNsWithinOpEntry(t, iOpJSON, ns)
}

func createUpdateOp(t *testing.T, ns string) *db.Oplog {
	t.Helper()

	uOpJSON := `
		{
		  "lsid": {
			"id": {
			  "$binary": {
				"base64": "HxXre7SSRxe8eq+OjOQOhw==",
				"subType": "04"
			  }
			},
			"uid": {
			  "$binary": {
				"base64": "Bh/Anp+//gSHltMgOtOX+7sunrF/VwW+VDdA3fRANl0=",
				"subType": "00"
			  }
			}
		  },
		  "txnNumber": {
			"$numberLong": "5"
		  },
		  "op": "u",
		  "ns": "db.coll",
		  "ui": {
			"$binary": {
			  "base64": "f774YvKERIKXSJVH+xCtPw==",
			  "subType": "04"
			}
		  },
		  "o": {
			"$v": 2,
			"diff": {
			  "i": {
				"city": "split"
			  }
			}
		  },
		  "o2": {
			"_id": {
			  "$oid": "6728e3fcedfb509c06f01307"
			}
		  },
		  "stmtId": 0,
		  "ts": {
			"$timestamp": {
			  "t": 1730733212,
			  "i": 1
			}
		  },
		  "t": {
			"$numberLong": "7"
		  },
		  "v": {
			"$numberLong": "2"
		  },
		  "wall": {
			"$date": "2024-11-04T15:13:32.123Z"
		  },
		  "prevOpTime": {
			"ts": {
			  "$timestamp": {
				"t": 0,
				"i": 0
			  }
			},
			"t": {
			  "$numberLong": "-1"
			}
		  }
		}`
	return replaceNsWithinOpEntry(t, uOpJSON, ns)
}

func createDeleteOp(t *testing.T, ns string) *db.Oplog {
	t.Helper()

	dOpJSON := `
		{
		  "lsid": {
			"id": {
			  "$binary": {
				"base64": "HxXre7SSRxe8eq+OjOQOhw==",
				"subType": "04"
			  }
			},
			"uid": {
			  "$binary": {
				"base64": "Bh/Anp+//gSHltMgOtOX+7sunrF/VwW+VDdA3fRANl0=",
				"subType": "00"
			  }
			}
		  },
		  "txnNumber": {
			"$numberLong": "6"
		  },
		  "op": "d",
		  "ns": "db.coll",
		  "ui": {
			"$binary": {
			  "base64": "f774YvKERIKXSJVH+xCtPw==",
			  "subType": "04"
			}
		  },
		  "o": {
			"_id": {
			  "$oid": "6728e3fcedfb509c06f01307"
			}
		  },
		  "stmtId": 0,
		  "ts": {
			"$timestamp": {
			  "t": 1730733256,
			  "i": 1
			}
		  },
		  "t": {
			"$numberLong": "7"
		  },
		  "v": {
			"$numberLong": "2"
		  },
		  "wall": {
			"$date": "2024-11-04T15:14:16.626Z"
		  },
		  "prevOpTime": {
			"ts": {
			  "$timestamp": {
				"t": 0,
				"i": 0
			  }
			},
			"t": {
			  "$numberLong": "-1"
			}
		  }
		}`

	return replaceNsWithinOpEntry(t, dOpJSON, ns)
}

func replaceNsWithinOpEntry(t *testing.T, jsonEntry, ns string) *db.Oplog {
	t.Helper()

	oe := db.Oplog{}
	err := bson.UnmarshalExtJSON([]byte(jsonEntry), false, &oe)
	if err != nil {
		t.Errorf("err while unmarshal from json: %v", err)
	}

	if ns != "" {
		oe.Namespace = ns
	}
	return &oe
}

func configSettingsBalancerEntry() *db.Oplog {
	return &db.Oplog{
		Operation: "i",
		Namespace: "config.settings",
		Object:    bson.D{{"_id", "balancer"}, {"mode", "off"}, {"stopped", true}},
		Query:     bson.D{{"_id", "balancer"}},
	}
}

func configSettingsAutomergeEntry() *db.Oplog {
	return &db.Oplog{
		Operation: "u",
		Namespace: "config.settings",
		Query:     bson.D{{"_id", "automerge"}},
	}
}

func configSettingsAnyOtherEntry() *db.Oplog {
	return &db.Oplog{
		Operation: "i",
		Namespace: "config.settings",
		Object:    bson.D{{"_id", "chunksize"}, {"value", 128}},
		Query:     bson.D{{"_id", "chunksize"}},
	}
}

func createConfigCollectionsEntry(shardedColl, collUUID string) *db.Oplog {
	uuid, _ := hex.DecodeString(collUUID)
	return &db.Oplog{
		Operation: "i",
		Namespace: "config.collections",
		Object: bson.D{
			{"_id", shardedColl},
			{"uuid", primitive.Binary{Subtype: bson.TypeBinaryUUID, Data: uuid}},
		},
	}
}

func createConfigChunksEntry(uuid string) *db.Oplog {
	uuidDecoded, _ := hex.DecodeString(uuid)
	id, _ := hex.DecodeString("some id")
	return &db.Oplog{
		Operation: "i",
		Namespace: "config.chunks",
		Object: bson.D{
			{"_id", id},
			{"uuid", primitive.Binary{Subtype: bson.TypeBinaryUUID, Data: uuidDecoded}},
			{"shard", "rsX"},
		},
	}
}
