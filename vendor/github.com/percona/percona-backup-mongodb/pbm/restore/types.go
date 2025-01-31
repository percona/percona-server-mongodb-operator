package restore

import (
	"sort"

	"github.com/mongodb/mongo-tools/common/db"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/restore/phys"
)

type RestoreMeta struct {
	Status           defs.Status         `bson:"status" json:"status"`
	Error            string              `bson:"error,omitempty" json:"error,omitempty"`
	Name             string              `bson:"name" json:"name"`
	OPID             string              `bson:"opid" json:"opid"`
	Backup           string              `bson:"backup" json:"backup"`
	BcpChain         []string            `bson:"bcp_chain" json:"bcp_chain"` // for incremental
	Namespaces       []string            `bson:"nss,omitempty" json:"nss,omitempty"`
	StartPITR        int64               `bson:"start_pitr" json:"start_pitr"`
	PITR             int64               `bson:"pitr" json:"pitr"`
	Replsets         []RestoreReplset    `bson:"replsets" json:"replsets"`
	Hb               primitive.Timestamp `bson:"hb" json:"hb"`
	StartTS          int64               `bson:"start_ts" json:"start_ts"`
	LastTransitionTS int64               `bson:"last_transition_ts" json:"last_transition_ts"`
	Conditions       Conditions          `bson:"conditions" json:"conditions"`
	Type             defs.BackupType     `bson:"type" json:"type"`
	Leader           string              `bson:"l,omitempty" json:"l,omitempty"`
	Stat             *phys.RestoreStat   `bson:"stat,omitempty" json:"stat,omitempty"`
}

type RestoreReplset struct {
	Name             string                `bson:"name" json:"name"`
	StartTS          int64                 `bson:"start_ts" json:"start_ts"`
	Status           defs.Status           `bson:"status" json:"status"`
	CommittedTxn     []phys.RestoreTxn     `bson:"committed_txn" json:"committed_txn"`
	CommittedTxnSet  bool                  `bson:"txn_set" json:"txn_set"`
	PartialTxn       []db.Oplog            `bson:"partial_txn" json:"partial_txn"`
	CurrentOp        primitive.Timestamp   `bson:"op" json:"op"`
	LastTransitionTS int64                 `bson:"last_transition_ts" json:"last_transition_ts"`
	LastWriteTS      primitive.Timestamp   `bson:"last_write_ts" json:"last_write_ts"`
	Nodes            []RestoreNode         `bson:"nodes,omitempty" json:"nodes,omitempty"`
	Error            string                `bson:"error,omitempty" json:"error,omitempty"`
	Conditions       Conditions            `bson:"conditions" json:"conditions"`
	Hb               primitive.Timestamp   `bson:"hb" json:"hb"`
	Stat             phys.RestoreShardStat `bson:"stat" json:"stat"`
}

type Condition struct {
	Timestamp int64       `bson:"timestamp" json:"timestamp"`
	Status    defs.Status `bson:"status" json:"status"`
	Error     string      `bson:"error,omitempty" json:"error,omitempty"`
}

type Conditions []*Condition

func (b Conditions) Len() int           { return len(b) }
func (b Conditions) Less(i, j int) bool { return b[i].Timestamp < b[j].Timestamp }
func (b Conditions) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }

// Insert keeps conditions asc sorted by Timestamp
func (b *Conditions) Insert(c *Condition) {
	i := sort.Search(len(*b), func(i int) bool { return []*Condition(*b)[i].Timestamp >= c.Timestamp })
	*b = append(*b, &Condition{})
	copy([]*Condition(*b)[i+1:], []*Condition(*b)[i:])
	[]*Condition(*b)[i] = c
}

type RestoreNode struct {
	Name             string              `bson:"name" json:"name"`
	Status           defs.Status         `bson:"status" json:"status"`
	LastTransitionTS int64               `bson:"last_transition_ts" json:"last_transition_ts"`
	Error            string              `bson:"error,omitempty" json:"error,omitempty"`
	Conditions       Conditions          `bson:"conditions" json:"conditions"`
	Hb               primitive.Timestamp `bson:"hb" json:"hb"`
}
