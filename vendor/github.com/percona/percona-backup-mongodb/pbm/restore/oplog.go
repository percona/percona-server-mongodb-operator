package restore

import (
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/mongodb/mongo-tools-common/db"
	"github.com/mongodb/mongo-tools-common/txn"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/percona/percona-backup-mongodb/pbm"
)

var skipNs = map[string]struct{}{
	"config.system.sessions":   {},
	"config.cache.collections": {},
	"config.mongos":            {},
	"config.lockpings":         {},
}

func init() {
	// add to skip collections that would be skiped while dump restore
	for _, v := range excludeFromDumpRestore {
		skipNs[v] = struct{}{}
	}
}

// Oplog is the oplog applyer
type Oplog struct {
	dst               *pbm.Node
	sv                *pbm.MongoVersion
	txnBuffer         *txn.Buffer
	needIdxWorkaround bool
	preserveUUID      bool
}

// NewOplog creates an object for an oplog applying
func NewOplog(dst *pbm.Node, sv *pbm.MongoVersion, preserveUUID bool) *Oplog {
	return &Oplog{
		dst:               dst,
		sv:                sv,
		preserveUUID:      preserveUUID,
		needIdxWorkaround: needsCreateIndexWorkaround(sv),
	}
}

// Apply applys an oplog from a given source
func (o *Oplog) Apply(src io.ReadCloser) error {
	bsonSource := db.NewDecodedBSONSource(db.NewBufferlessBSONSource(src))
	defer bsonSource.Close()

	o.txnBuffer = txn.NewBuffer()
	defer o.txnBuffer.Stop()

	for {
		rawOplogEntry := bsonSource.LoadNext()
		if rawOplogEntry == nil {
			break
		}
		oe := db.Oplog{}
		err := bson.Unmarshal(rawOplogEntry, &oe)
		if err != nil {
			return errors.Wrap(err, "reading oplog")
		}

		if _, ok := skipNs[oe.Namespace]; ok {
			continue
		}

		//skip no-ops
		if oe.Operation == "n" {
			continue
		}

		meta, err := txn.NewMeta(oe)
		if err != nil {
			return errors.Wrap(err, "getting op metadata")
		}

		if meta.IsTxn() {
			err = o.handleTxnOp(meta, oe)
			if err != nil {
				return errors.Wrap(err, "applying a transaction entry")
			}
		} else {
			err = o.handleNonTxnOp(oe)
			if err != nil {
				return errors.Wrap(err, "applying an entry")
			}
		}
	}

	return nil
}

func (o *Oplog) handleTxnOp(meta txn.Meta, op db.Oplog) error {
	err := o.txnBuffer.AddOp(meta, op)
	if err != nil {
		return errors.Wrap(err, "buffering entry")
	}

	if meta.IsAbort() {
		err := o.txnBuffer.PurgeTxn(meta)
		if err != nil {
			return errors.Wrap(err, "cleaning up transaction buffer on abort")
		}
		return nil
	}

	if !meta.IsCommit() {
		return nil
	}

	// From here, we're applying transaction entries
	ops, errs := o.txnBuffer.GetTxnStream(meta)

Loop:
	for {
		select {
		case op, ok := <-ops:
			if !ok {
				break Loop
			}
			err = o.handleNonTxnOp(op)
			if err != nil {
				return errors.Wrap(err, "applying transaction op")
			}
		case err := <-errs:
			if err != nil {
				return errors.Wrap(err, "replaying transaction")
			}
			break Loop
		}
	}

	err = o.txnBuffer.PurgeTxn(meta)
	if err != nil {
		return errors.Wrap(err, "cleaning up transaction buffer")
	}

	return nil
}

func (o *Oplog) handleNonTxnOp(op db.Oplog) error {
	op, err := o.filterUUIDs(op)
	if err != nil {
		return errors.Wrap(err, "filtering UUIDs from oplog")
	}

	return o.applyOps([]interface{}{op})
}

// applyOps is a wrapper for the applyOps database command, we pass in
// a session to avoid opening a new connection for a few inserts at a time.
func (o *Oplog) applyOps(entries []interface{}) error {
	singleRes := o.dst.Session().Database("admin").RunCommand(nil, bson.D{{"applyOps", entries}})
	if err := singleRes.Err(); err != nil {
		return errors.Wrap(err, "applyOps")
	}
	res := bson.M{}
	singleRes.Decode(&res)
	if isFalsy(res["ok"]) {
		return errors.Errorf("applyOps command: %v", res["errmsg"])
	}

	return nil
}

// filterUUIDs removes 'ui' entries from ops, including nested applyOps ops.
// It also modifies ops that rely on 'ui'.
func (o *Oplog) filterUUIDs(op db.Oplog) (db.Oplog, error) {
	// Remove UUIDs from oplog entries
	if !o.preserveUUID {
		op.UI = nil

		// The createIndexes oplog command requires 'ui' for some server versions, so
		// in that case we fall back to an old-style system.indexes insert.
		if op.Operation == "c" && op.Object[0].Key == "createIndexes" && o.needIdxWorkaround {
			return convertCreateIndexToIndexInsert(op)
		}
	}

	// Check for and filter nested applyOps ops
	if op.Operation == "c" && isApplyOpsCmd(op.Object) {
		filtered, err := o.newFilteredApplyOps(op.Object)
		if err != nil {
			return db.Oplog{}, err
		}
		op.Object = filtered
	}

	return op, nil
}

// newFilteredApplyOps iterates over nested ops in an applyOps document and
// returns a new applyOps document that omits the 'ui' field from nested ops.
func (o *Oplog) newFilteredApplyOps(cmd bson.D) (bson.D, error) {
	ops, err := unwrapNestedApplyOps(cmd)
	if err != nil {
		return nil, err
	}

	filtered := make([]db.Oplog, len(ops))
	for i, v := range ops {
		filtered[i], err = o.filterUUIDs(v)
		if err != nil {
			return nil, err
		}
	}

	doc, err := wrapNestedApplyOps(filtered)
	if err != nil {
		return nil, err
	}

	return doc, nil
}

// server versions 3.6.0-3.6.8 and 4.0.0-4.0.2 require a 'ui' field
// in the createIndexes command.
func needsCreateIndexWorkaround(sver *pbm.MongoVersion) bool {
	v := sver.Version
	if len(v) < 3 {
		return false
	}
	sv := db.Version{v[0], v[1], v[2]}
	if (sv.GTE(db.Version{3, 6, 0}) && sv.LTE(db.Version{3, 6, 8})) ||
		(sv.GTE(db.Version{4, 0, 0}) && sv.LTE(db.Version{4, 0, 2})) {
		return true
	}
	return false
}

// convertCreateIndexToIndexInsert converts from new-style create indexes
// command to old style special index insert.
func convertCreateIndexToIndexInsert(op db.Oplog) (db.Oplog, error) {
	dbName := op.Namespace
	if i := strings.Index(dbName, "."); i > -1 {
		dbName = dbName[:i]
	}

	cmdValue := op.Object[0].Value
	collName, ok := cmdValue.(string)
	if !ok {
		return db.Oplog{}, errors.New("unknown format for createIndexes")
	}

	indexSpec := op.Object[1:]
	if len(indexSpec) < 3 {
		return db.Oplog{}, errors.New("unknown format for createIndexes, index spec " +
			"must have at least \"v\", \"key\", and \"name\" fields")
	}

	// createIndexes does not include the "ns" field but index inserts
	// do. Add it as the third field, after "v", "key", and "name".
	ns := bson.D{{"ns", fmt.Sprintf("%s.%s", dbName, collName)}}
	indexSpec = append(indexSpec[:3], append(ns, indexSpec[3:]...)...)
	op.Object = indexSpec
	op.Namespace = fmt.Sprintf("%s.system.indexes", dbName)
	op.Operation = "i"

	return op, nil
}

// isApplyOpsCmd returns true if a document seems to be an applyOps command.
func isApplyOpsCmd(cmd bson.D) bool {
	for _, v := range cmd {
		if v.Key == "applyOps" {
			return true
		}
	}
	return false
}

// nestedApplyOps models an applyOps command document
type nestedApplyOps struct {
	ApplyOps []db.Oplog `bson:"applyOps"`
}

// unwrapNestedApplyOps converts a bson.D to a typed data structure.
// Unfortunately, we're forced to convert by marshaling to bytes and
// unmarshalling.
func unwrapNestedApplyOps(doc bson.D) ([]db.Oplog, error) {
	// Doc to bytes
	bs, err := bson.Marshal(doc)
	if err != nil {
		return nil, errors.Wrap(err, "remarshal nested applyOps")
	}

	// Bytes to typed data
	var cmd nestedApplyOps
	err = bson.Unmarshal(bs, &cmd)
	if err != nil {
		return nil, errors.Wrap(err, "unwrap nested applyOps")
	}

	return cmd.ApplyOps, nil
}

// wrapNestedApplyOps converts a typed data structure to a bson.D.
// Unfortunately, we're forced to convert by marshaling to bytes and
// unmarshalling.
func wrapNestedApplyOps(ops []db.Oplog) (bson.D, error) {
	cmd := &nestedApplyOps{ApplyOps: ops}

	// Typed data to bytes
	raw, err := bson.Marshal(cmd)
	if err != nil {
		return nil, errors.Wrap(err, "rewrap nested applyOps op")
	}

	// Bytes to doc
	var doc bson.D
	err = bson.Unmarshal(raw, &doc)
	if err != nil {
		return nil, errors.Wrap(err, "reunmarshal nested applyOps op")
	}

	return doc, nil
}

// isTruthy returns true for values the server will interpret as "true".
// True values include {}, [], "", true, and any numbers != 0
func isTruthy(val interface{}) bool {
	if val == nil {
		return false
	}
	if val == (primitive.Undefined{}) {
		return false
	}

	v := reflect.ValueOf(val)
	switch v.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array, reflect.String, reflect.Struct:
		return true
	default:
		z := reflect.Zero(v.Type())
		return v.Interface() != z.Interface()
	}
}

// isFalsy returns true for values the server will interpret as "false".
// False values include numbers == 0, false, and nil
func isFalsy(val interface{}) bool {
	return !isTruthy(val)
}
