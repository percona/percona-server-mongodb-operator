package healthcheck

import (
	"context"
	"encoding/json"

	v "github.com/hashicorp/go-version"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func getStatus(ctx context.Context, client mongo.Client) (ReplSetStatus, error) {
	buildInfo, err := client.RSBuildInfo(ctx)
	if err != nil {
		return ReplSetStatus{}, errors.Wrap(err, "get buildInfo response")
	}

	replSetStatusCommand := bson.D{{Key: "replSetGetStatus", Value: 1}}
	mongoVersion := v.Must(v.NewVersion(buildInfo.Version))
	if mongoVersion.Compare(v.Must(v.NewVersion("4.2.1"))) < 0 {
		// https://docs.mongodb.com/manual/reference/command/replSetGetStatus/#syntax
		replSetStatusCommand = append(replSetStatusCommand, primitive.E{Key: "initialSync", Value: 1})
	}

	res := client.Database("admin").RunCommand(ctx, replSetStatusCommand)
	if res.Err() != nil {
		if res.Err().Error() == ErrNoReplsetConfigStr {
			return ReplSetStatus{}, errors.New(ErrNoReplsetConfigStr)
		}
		return ReplSetStatus{}, errors.Wrap(res.Err(), "get replsetGetStatus response")
	}

	// this is a workaround to fix decoding of empty interfaces
	// https://jira.mongodb.org/browse/GODRIVER-988
	rsStatus := ReplSetStatus{}
	tempResult := bson.M{}
	if err := res.Decode(&tempResult); err != nil {
		return ReplSetStatus{}, errors.Wrap(err, "decode replsetGetStatus response")
	}
	result, err := json.Marshal(tempResult)
	if err != nil {
		return ReplSetStatus{}, errors.Wrap(err, "marshal temp result")
	}
	if err = json.Unmarshal(result, &rsStatus); err != nil {
		return ReplSetStatus{}, errors.Wrap(err, "unmarshal temp result")
	}
	return rsStatus, nil
}
