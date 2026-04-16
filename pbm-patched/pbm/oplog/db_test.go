package oplog

import (
	"context"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestGetUUIDForNS(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))

	mt.Run("successful response from db", func(mt *mtest.T) {
		expectedUUID := primitive.Binary{Subtype: 0xFF, Data: []byte{0x01, 0x02, 0x03}}
		listCollRes := bson.D{
			{"name", "c1"},
			{"type", "collection"},
			{"info", bson.D{
				{"readOnly", false},
				{"uuid", expectedUUID},
			}},
		}
		mt.AddMockResponses(mtest.CreateCursorResponse(1, "mydb.c1", mtest.FirstBatch, listCollRes))

		db := newMDB(mt.Client)
		uuid, err := db.getUUIDForNS(context.Background(), "mydb.c1")
		if err != nil {
			t.Errorf("got err=%v", err)
		}
		primitive.NewObjectID()

		if !uuid.Equal(expectedUUID) {
			t.Errorf("wrong uuid for ns: expected=%v, got=%v", expectedUUID, uuid)
		}
		t.Log(uuid)
	})

	mt.Run("failed response from db", func(mt *mtest.T) {
		errRes := mtest.CreateCommandErrorResponse(mtest.CommandError{
			Code:    11601,
			Name:    "error",
			Message: "querying list collections",
		})
		mt.AddMockResponses(errRes)
		db := newMDB(mt.Client)
		_, err := db.getUUIDForNS(context.Background(), "mydb.c1")
		if err == nil {
			t.Error("expected to get error from getUUIDForNS")
		}
		if !strings.Contains(err.Error(), "list collections") {
			t.Error("wrong err")
		}
	})
}
