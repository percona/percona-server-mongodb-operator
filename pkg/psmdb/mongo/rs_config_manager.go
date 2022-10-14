package mongo

import (
	"context"
	"fmt"
	"reflect"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
)

type RSConfigManager interface {
	ReadConfig(ctx context.Context) (RSConfig, error)
	WriteConfig(ctx context.Context, cfg RSConfig) error
	SetDefaultRWConcern(ctx context.Context, readConcern, writeConcern string) error
}

func (c *client) ReadConfig(ctx context.Context) (RSConfig, error) {
	resp := ReplSetGetConfig{}
	res := c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetGetConfig", Value: 1}})
	if res.Err() != nil {
		return RSConfig{}, errors.Wrap(res.Err(), "replSetGetConfig")
	}
	if err := res.Decode(&resp); err != nil {
		return RSConfig{}, errors.Wrap(err, "failed to decode to replSetGetConfig")
	}

	if resp.Config == nil {
		return RSConfig{}, errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return *resp.Config, nil
}

func (c *client) WriteConfig(ctx context.Context, cfg RSConfig) error {
	resp := OKResponse{}

	log.V(1).Info("Running replSetReconfig config", "cfg", cfg)

	res := c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetReconfig", Value: cfg}})
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "replSetReconfig")
	}

	if err := res.Decode(&resp); err != nil {
		return errors.Wrap(err, "failed to decode to replSetReconfigResponse")
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

// RemoveOld removes from the list those members which are not present in the given list.
// It always should leave at least one element. The config won't be valid for mongo otherwise.
// Better, if the last element has the smallest ID in order not to produce defragmentation
// when the next element will be added (ID = maxID + 1). Mongo replica set member ID must be between 0 and 255, so it matters.
func (m *ConfigMembers) RemoveOld(compareWith ConfigMembers) bool {
	cm := make(map[string]struct{}, len(compareWith))

	for _, member := range compareWith {
		cm[member.Host] = struct{}{}
	}

	// going from the end to the starting in order to leave last element with the smallest id
	for i := len(*m) - 1; i >= 0 && len(*m) > 1; i-- {
		member := []ConfigMember(*m)[i]
		if _, ok := cm[member.Host]; !ok {
			*m = append([]ConfigMember(*m)[:i], []ConfigMember(*m)[i+1:]...)
			return true
		}
	}

	return false
}

func (m *ConfigMembers) FixHosts(compareWith ConfigMembers) (changes bool) {
	if len(*m) < 1 {
		return changes
	}

	cm := make(map[string]string, len(compareWith))

	for _, member := range compareWith {
		name, ok := member.Tags["podName"]
		if !ok {
			continue
		}
		cm[name] = member.Host
	}

	for i := 0; i < len(*m); i++ {
		member := []ConfigMember(*m)[i]
		podName, ok := member.Tags["podName"]
		if !ok {
			continue
		}
		if host, ok := cm[podName]; ok && host != member.Host {
			changes = true
			[]ConfigMember(*m)[i].Host = host
		}
	}

	return changes
}

// FixTags corrects the tags of any member if they changed.
// Especially the "external" tag can change if cluster is switched from
// unmanaged to managed.
func (m *ConfigMembers) FixTags(compareWith ConfigMembers) (changes bool) {
	if len(*m) < 1 {
		return changes
	}

	cm := make(map[string]ReplsetTags, len(compareWith))

	for _, member := range compareWith {
		if member.ArbiterOnly {
			continue
		}
		cm[member.Host] = member.Tags
	}

	for i := 0; i < len(*m); i++ {
		member := []ConfigMember(*m)[i]
		if c, ok := cm[member.Host]; ok && !reflect.DeepEqual(member.Tags, c) {
			changes = true
			[]ConfigMember(*m)[i].Tags = c
		}
	}

	return changes
}

// ExternalNodesChanged checks if votes or priority fields changed for external nodes
func (m *ConfigMembers) ExternalNodesChanged(compareWith ConfigMembers) bool {
	cm := make(map[string]struct {
		votes    int
		priority int
	}, len(compareWith))

	for _, member := range compareWith {
		_, ok := member.Tags["external"]
		if !ok {
			continue
		}
		cm[member.Host] = struct {
			votes    int
			priority int
		}{votes: member.Votes, priority: member.Priority}
	}

	for i := 0; i < len(*m); i++ {
		member := []ConfigMember(*m)[i]
		if ext, ok := cm[member.Host]; ok {
			if ext.votes != member.Votes || ext.priority != member.Priority {
				[]ConfigMember(*m)[i].Votes = ext.votes
				[]ConfigMember(*m)[i].Priority = ext.priority

				return true
			}
		}
	}

	return false
}

// AddNew adds a new member from given list to the config.
// It adds only one at a time. Returns true if it adds any member.
func (m *ConfigMembers) AddNew(from ConfigMembers) bool {
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
			return true
		}
	}

	return false
}

// SetVotes sets voting parameters for members list
func (m *ConfigMembers) SetVotes(unsafePSA bool) {
	votes := 0
	lastVoteIdx := -1
	for i, member := range *m {
		if member.Hidden {
			continue
		}

		if _, ok := member.Tags["external"]; ok {
			[]ConfigMember(*m)[i].Votes = member.Votes
			[]ConfigMember(*m)[i].Priority = member.Priority

			if member.Votes == 1 {
				votes++
			}

			continue
		}

		if _, ok := member.Tags["nonVoting"]; ok {
			// Non voting member is a regular ReplSet member with
			// votes and priority equals to 0.

			[]ConfigMember(*m)[i].Votes = 0
			[]ConfigMember(*m)[i].Priority = 0

			continue
		}

		if votes < MaxVotingMembers {
			[]ConfigMember(*m)[i].Votes = 1
			votes++

			if !member.ArbiterOnly {
				lastVoteIdx = i
				// Priority can be any number in range [0,1000].
				// We're setting it to 2 as default, to allow
				// users to configure external nodes with lower
				// priority than local nodes.
				if !unsafePSA || member.Votes == 1 {
					// In unsafe PSA (Primary with a Secondary and an Arbiter),
					// we are unable to set the votes and the priority simultaneously.
					// Therefore, setting only the votes.
					[]ConfigMember(*m)[i].Priority = DefaultPriority
				}
			}
		} else if member.ArbiterOnly {
			// Arbiter should always have a vote
			[]ConfigMember(*m)[i].Votes = 1

			// We're over the max voters limit. Make room for the arbiter
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
