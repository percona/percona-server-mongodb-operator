package mongo

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	corev1 "k8s.io/api/core/v1"
)

func Dial(addrs []string, replset string, usersSecret *corev1.Secret, useTLS bool) (*mongo.Client, error) {
	ctx, connectcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer connectcancel()

	opts := options.Client().
		SetHosts(addrs).
		SetReplicaSet(replset).
		SetAuth(options.Credential{
			Password: string(usersSecret.Data[envMongoDBClusterAdminPassword]),
			Username: string(usersSecret.Data[envMongoDBClusterAdminUser]),
		}).
		SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))).
		SetReadPreference(readpref.Primary())

	if useTLS {
		tlsCfg := tls.Config{InsecureSkipVerify: true}
		opts = opts.SetTLSConfig(&tlsCfg).SetDialer(tlsDialer{cfg: &tlsCfg})
	}

	client, err := mongo.Connect(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mongo rs: %v", err)
	}

	ctx, pingcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer pingcancel()

	err = client.Ping(ctx, readpref.Primary())
	if err != nil {
		return nil, fmt.Errorf("failed to ping mongo: %v", err)
	}

	return client, nil
}

type tlsDialer struct {
	cfg *tls.Config
}

func (d tlsDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return tls.Dial("tcp", address, d.cfg)
}

func ReadConfig(ctx context.Context, client *mongo.Client) (RSConfig, error) {
	resp := ReplSetGetConfig{}
	res := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetConfig", Value: 1}})
	if res.Err() != nil {
		return RSConfig{}, errors.Wrap(res.Err(), "replSetGetConfig")
	}
	if err := res.Decode(&resp); err != nil {
		return RSConfig{}, errors.Wrap(err, "failed to decoge to replSetGetConfig")
	}

	if resp.Config == nil {
		return RSConfig{}, fmt.Errorf("mongo says: %s", resp.Errmsg)
	}

	return *resp.Config, nil
}

func WriteConfig(ctx context.Context, client *mongo.Client, cfg RSConfig) error {
	resp := OKResponse{}

	// TODO The 'force' flag should be set to true if there is no PRIMARY in the replset (but this shouldn't ever happen).
	res := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetReconfig", Value: cfg}, {Key: "force", Value: false}})
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "replSetReconfig")
	}

	if err := res.Decode(&resp); err != nil {
		return errors.Wrap(err, "failed to decoge to replSetReconfigResponce")
	}

	if resp.OK != 1 {
		return fmt.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func RSStatus(ctx context.Context, client *mongo.Client) (Status, error) {
	status := Status{}

	resp := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetStatus", Value: 1}})
	if resp.Err() != nil {
		return status, errors.Wrap(resp.Err(), "replSetGetStatus")
	}

	if err := resp.Decode(&status); err != nil {
		return status, errors.Wrap(err, "failed to decode rs status")
	}

	if status.OK != 1 {
		return status, fmt.Errorf("mongo says: %s", status.Errmsg)
	}

	return status, nil
}

func RSBuildInfo(ctx context.Context, client *mongo.Client) (BuildInfo, error) {
	bi := BuildInfo{}

	resp := client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildinfo", Value: 1}})
	if resp.Err() != nil {
		return bi, errors.Wrap(resp.Err(), "buildinfo")
	}

	if err := resp.Decode(&bi); err != nil {
		return bi, errors.Wrap(err, "failed to decode build info")
	}

	if bi.OK != 1 {
		return bi, fmt.Errorf("mongo says: %s", bi.Errmsg)
	}

	return bi, nil
}

func StepDown(ctx context.Context, client *mongo.Client) error {
	resp := OKResponse{}

	res := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetStepDown", Value: 60}})
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "replSetStepDown")
	}

	if err := res.Decode(&resp); err != nil {
		return errors.Wrap(err, "failed to decode responce of replSetStepDown")
	}

	if resp.OK != 1 {
		return fmt.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

// RemoveOld removes from the list those members which are not present in the given list.
// It always should leave at least one element. The config won't be valid for mongo otherwise.
// Better, if the last element has the smallest ID in order not to produce defragmentation
// when the next element will be added (ID = maxID + 1). Mongo replica set member ID must be between 0 and 255, so it matters.
func (m *ConfigMembers) RemoveOld(compareWith ConfigMembers) (changes bool) {
	cm := make(map[string]struct{}, len(compareWith))

	for _, member := range compareWith {
		cm[member.Host] = struct{}{}
	}

	// going from the end to the starting in order to leave last element with the smallest id
	for i := len(*m) - 1; i >= 0 && len(*m) > 1; i-- {
		member := []ConfigMember(*m)[i]
		if _, ok := cm[member.Host]; !ok {
			*m = append([]ConfigMember(*m)[:i], []ConfigMember(*m)[i+1:]...)
			changes = true
		}
	}

	return changes
}

// AddNew adds new members from given list
func (m *ConfigMembers) AddNew(from ConfigMembers) (changes bool) {
	cm := make(map[string]struct{}, len(*m))
	lastID := 0

	for _, member := range *m {
		cm[member.Host] = struct{}{}
		if member.ID > lastID {
			lastID = member.ID
		}
	}

	for _, member := range from {
		if _, ok := cm[member.Host]; !ok {
			lastID++
			member.ID = lastID
			*m = append(*m, member)
			changes = true
		}
	}

	return changes
}

// SetVotes sets voting parameters for members list
func (m *ConfigMembers) SetVotes() {
	votes := 0
	lastVoteIdx := -1
	for i, member := range *m {
		if member.Hidden {
			continue
		}
		if votes < MaxVotingMembers {
			[]ConfigMember(*m)[i].Votes = 1
			votes++
			if !member.ArbiterOnly {
				lastVoteIdx = i
				[]ConfigMember(*m)[i].Priority = 1
			}
		} else if member.ArbiterOnly {
			// Arbiter should always have a vote
			[]ConfigMember(*m)[i].Votes = 1
			[]ConfigMember(*m)[lastVoteIdx].Votes = 0
			[]ConfigMember(*m)[lastVoteIdx].Priority = 0
		}
	}
	if votes == 0 {
		return
	}

	if votes%2 == 0 {
		[]ConfigMember(*m)[lastVoteIdx].Votes = 0
		[]ConfigMember(*m)[lastVoteIdx].Priority = 0
	}
}

func (m ConfigMember) String() string {
	return fmt.Sprintf("{votes: %d, priority: %d}", m.Votes, m.Priority)
}
