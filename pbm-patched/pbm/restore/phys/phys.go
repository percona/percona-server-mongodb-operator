package phys

import (
	"bytes"
	"fmt"
	"strconv"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

type RestoreStat struct {
	RS map[string]map[string]RestoreRSMetrics `bson:"rs,omitempty" json:"rs,omitempty"`
}
type RestoreRSMetrics struct {
	DistTxn  DistTxnStat          `bson:"txn,omitempty" json:"txn,omitempty"`
	Download storage.DownloadStat `bson:"download,omitempty" json:"download,omitempty"`
}

type DistTxnStat struct {
	// Partial is the num of transactions that were allied on other shards
	// but can't be applied on this one since not all prepare messages got
	// into the oplog (shouldn't happen).
	Partial int `bson:"partial" json:"partial"`
	// ShardUncommitted is the number of uncommitted transactions before
	// the sync. Basically, the transaction is full but no commit message
	// in the oplog of this shard.
	ShardUncommitted int `bson:"shard_uncommitted" json:"shard_uncommitted"`
	// LeftUncommitted is the num of transactions that remain uncommitted
	// after the sync. The transaction is full but no commit message in the
	// oplog of any shard.
	LeftUncommitted int `bson:"left_uncommitted" json:"left_uncommitted"`
}

type RestoreShardStat struct {
	Txn DistTxnStat           `json:"txn"`
	D   *storage.DownloadStat `json:"d"`
}

type TxnState string

const (
	TxnCommit  TxnState = "commit"
	TxnPrepare TxnState = "prepare"
	TxnAbort   TxnState = "abort"
	TxnUnknown TxnState = ""
)

type RestoreTxn struct {
	ID    string              `bson:"id" json:"id"`
	Ctime primitive.Timestamp `bson:"ts" json:"ts"` // commit timestamp of the transaction
	State TxnState            `bson:"state" json:"state"`
}

func (t RestoreTxn) Encode() []byte {
	return []byte(fmt.Sprintf("txn:%d,%d:%s:%s", t.Ctime.T, t.Ctime.I, t.ID, t.State))
}

func (t *RestoreTxn) Decode(b []byte) error {
	for k, v := range bytes.SplitN(bytes.TrimSpace(b), []byte{':'}, 4) {
		switch k {
		case 0:
		case 1:
			if si := bytes.SplitN(v, []byte{','}, 2); len(si) == 2 {
				tt, err := strconv.ParseInt(string(si[0]), 10, 64)
				if err != nil {
					return errors.Wrap(err, "parse clusterTime T")
				}
				ti, err := strconv.ParseInt(string(si[1]), 10, 64)
				if err != nil {
					return errors.Wrap(err, "parse clusterTime I")
				}

				t.Ctime = primitive.Timestamp{T: uint32(tt), I: uint32(ti)}
			}
		case 2:
			t.ID = string(v)
		case 3:
			t.State = TxnState(string(v))
		}
	}

	return nil
}

func (t RestoreTxn) String() string {
	return fmt.Sprintf("<%s> [%s] %v", t.ID, t.State, t.Ctime)
}
