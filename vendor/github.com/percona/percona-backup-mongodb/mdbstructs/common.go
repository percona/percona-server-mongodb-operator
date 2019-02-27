package mdbstructs

import (
	"time"

	"github.com/globalsign/mgo/bson"
)

type OkResponse struct {
	Ok            int                  `bson:"ok"`
	ClusterTime   *ClusterTime         `bson:"$clusterTime,omitempty"`
	OperationTime *bson.MongoTimestamp `bson:"operationTime,omitempty"`
}

type ClusterTime struct {
	ClusterTime time.Time `bson:"clusterTime"`
	Signature   struct {
		Hash  bson.Binary `bson:"hash"`
		KeyID int64       `bson:"keyId"`
	} `bson:"signature"`
}

type GleStats struct {
	LastOpTime bson.MongoTimestamp `bson:"lastOpTime"`
	ElectionId bson.ObjectId       `bson:"electionId"`
}

type OpTime struct {
	Ts   bson.MongoTimestamp `bson:"ts" json:"ts"`
	Term int64               `bson:"t" json:"t"`
}
