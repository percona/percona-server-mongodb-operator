package mdbstructs

import (
	"strings"
	"time"

	"github.com/globalsign/mgo/bson"
)

type Operation string

const (
	OperationInsert  Operation = "i"
	OperationNoop    Operation = "n"
	OperationUpdate  Operation = "u"
	OperationDelete  Operation = "d"
	OperationCommand Operation = "c"
)

var (
	operationStr = map[Operation]string{
		OperationInsert:  "insert",
		OperationNoop:    "noop",
		OperationUpdate:  "update",
		OperationDelete:  "delete",
		OperationCommand: "command",
	}
)

func (o Operation) String() string {
	if val, ok := operationStr[o]; ok {
		return val
	}
	return ""
}

type Namespace string

func (n Namespace) split() []string {
	return strings.SplitN(string(n), ".", 2)
}

func (n Namespace) Database() string {
	return n.split()[0]
}

func (n Namespace) Collection() string {
	return n.split()[1]
}

type Oplog struct {
	Timestamp bson.MongoTimestamp `bson:"ts"`
	HistoryID int64               `bson:"h"`
	Version   int                 `bson:"v"`
	Operation Operation           `bson:"op"`
	Namespace Namespace           `bson:"ns"`
	Object    bson.RawD           `bson:"o"`
	Query     bson.RawD           `bson:"o2,omitempty"`
	Term      int64               `bson:"t,omitempty"`
	UI        *bson.Binary        `bson:"ui,omitempty"`
	WallTime  time.Time           `bson:"wall,omitempty"`
}

type OplogTimestampOnly struct {
	Timestamp bson.MongoTimestamp `bson:"ts"`
}
