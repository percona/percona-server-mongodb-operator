package topo

import (
	"context"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

type ConnectionStatus struct {
	AuthInfo AuthInfo `bson:"authInfo" json:"authInfo"`
}

type AuthInfo struct {
	Users     []AuthUser      `bson:"authenticatedUsers" json:"authenticatedUsers"`
	UserRoles []AuthUserRoles `bson:"authenticatedUserRoles" json:"authenticatedUserRoles"`
}

type AuthUser struct {
	User string `bson:"user" json:"user"`
	DB   string `bson:"db" json:"db"`
}

type AuthUserRoles struct {
	Role string `bson:"role" json:"role"`
	DB   string `bson:"db" json:"db"`
}

func CheckTopoForBackup(ctx context.Context, m connect.Client, type_ defs.BackupType) error {
	members, err := ClusterMembers(ctx, m.MongoClient())
	if err != nil {
		return errors.Wrap(err, "get cluster members")
	}

	ts, err := GetClusterTime(ctx, m)
	if err != nil {
		return errors.Wrap(err, "get cluster time")
	}

	agentList, err := ListAgents(ctx, m)
	if err != nil {
		return errors.Wrap(err, "list agents")
	}

	agents := make(map[string]map[string]AgentStat)
	for _, a := range agentList {
		if agents[a.RS] == nil {
			agents[a.RS] = make(map[string]AgentStat)
		}
		agents[a.RS][a.Node] = a
	}

	return collectTopoCheckErrors(members, agents, ts, type_)
}

type (
	ReplsetName = string
	ShardName   = string
	NodeURI     = string
)

type topoCheckError struct {
	Replsets map[ReplsetName]map[NodeURI][]error
	Missed   []string
}

func (r topoCheckError) hasError() bool {
	return len(r.Missed) != 0
}

func (r topoCheckError) Error() string {
	if !r.hasError() {
		return ""
	}

	return fmt.Sprintf("no available agent(s) on replsets: %s", strings.Join(r.Missed, ", "))
}

func collectTopoCheckErrors(
	replsets []Shard,
	agentsByRS map[ReplsetName]map[NodeURI]AgentStat,
	ts primitive.Timestamp,
	type_ defs.BackupType,
) error {
	rv := topoCheckError{
		Replsets: make(map[string]map[NodeURI][]error),
		Missed:   make([]string, 0),
	}

	for _, rs := range replsets {
		rsName, uri, _ := strings.Cut(rs.Host, "/")
		agents := agentsByRS[rsName]
		if len(agents) == 0 {
			rv.Missed = append(rv.Missed, rsName)
			continue
		}

		hosts := strings.Split(uri, ",")
		members := make(map[NodeURI][]error, len(hosts))
		anyAvail := false
		for _, host := range hosts {
			a, ok := agents[host]
			if !ok || a.Arbiter || a.Passive {
				continue
			}

			errs := []error{}
			if a.Err != "" {
				errs = append(errs, errors.New(a.Err))
			}
			if ok, estrs := a.OK(); !ok {
				errs = append(errs, estrs...)
			}

			const maxReplicationLag uint32 = 35
			if ts.T-a.Heartbeat.T > maxReplicationLag {
				errs = append(errs, errors.New("stale"))
			}
			if err := version.FeatureSupport(a.MongoVersion()).BackupType(type_); err != nil {
				errs = append(errs, errors.Wrap(err, "unsupported backup type"))
			}

			members[host] = errs
			if len(errs) == 0 {
				anyAvail = true
			}
		}

		rv.Replsets[rsName] = members

		if !anyAvail {
			rv.Missed = append(rv.Missed, rsName)
		}
	}

	if rv.hasError() {
		return rv
	}

	return nil
}

// NodeSuits checks if node can perform backup
func NodeSuits(ctx context.Context, m *mongo.Client, inf *NodeInfo) (bool, error) {
	status, err := GetNodeStatus(ctx, m, inf.Me)
	if err != nil {
		return false, errors.Wrap(err, "get node status")
	}
	if status.IsArbiter() {
		return false, nil
	}

	replLag, err := ReplicationLag(ctx, m, inf.Me)
	if err != nil {
		return false, errors.Wrap(err, "get node replication lag")
	}

	return replLag < defs.MaxReplicationLagTimeSec && status.Health == defs.NodeHealthUp &&
			(status.State == defs.NodeStatePrimary || status.State == defs.NodeStateSecondary),
		nil
}

func NodeSuitsExt(ctx context.Context, m *mongo.Client, inf *NodeInfo, t defs.BackupType) (bool, error) {
	if ok, err := NodeSuits(ctx, m, inf); err != nil || !ok {
		return false, err
	}

	ver, err := version.GetMongoVersion(ctx, m)
	if err != nil {
		return false, errors.Wrap(err, "get mongo version")
	}

	err = version.FeatureSupport(ver).BackupType(t)
	return err == nil, err
}

// GetReplsetStatus returns `replSetGetStatus` for the given connection
func GetReplsetStatus(ctx context.Context, m *mongo.Client) (*ReplsetStatus, error) {
	status := &ReplsetStatus{}
	err := m.Database("admin").RunCommand(ctx, bson.D{{"replSetGetStatus", 1}}).Decode(status)
	if err != nil {
		return nil, errors.Wrap(err, "query adminCommand: replSetGetStatus")
	}

	return status, nil
}

func GetNodeStatus(ctx context.Context, m *mongo.Client, name string) (*NodeStatus, error) {
	s, err := GetReplsetStatus(ctx, m)
	if err != nil {
		return nil, errors.Wrap(err, "get replset status")
	}

	for _, m := range s.Members {
		if m.Name == name {
			return &m, nil
		}
	}

	return nil, errors.ErrNotFound
}

// ReplicationLag returns node replication lag in seconds
func ReplicationLag(ctx context.Context, m *mongo.Client, self string) (int, error) {
	s, err := GetReplsetStatus(ctx, m)
	if err != nil {
		return -1, errors.Wrap(err, "get replset status")
	}

	var primaryOptime, nodeOptime int
	for _, m := range s.Members {
		if m.Name == self {
			nodeOptime = int(m.Optime.TS.T)
		}
		if m.StateStr == "PRIMARY" {
			primaryOptime = int(m.Optime.TS.T)
		}
	}

	return primaryOptime - nodeOptime, nil
}
