package topo

import (
	"context"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/mod/semver"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

type AgentStat struct {
	// Node is like agent ID. Looks like `rs00:27017` (host:port of direct mongod).
	Node string `bson:"n"`

	// RS is the direct node replset name.
	RS string `bson:"rs"`

	// State is the mongod state code.
	State defs.NodeState `bson:"s"`

	// StateStr is the mongod state string (e.g. PRIMARY, SECONDARY, ARBITER)
	StateStr string `bson:"str"`

	// Hidden is set for hidden node.
	Hidden bool `bson:"hdn"`

	// Passive is set when node cannot be primary (priority 0).
	//
	// Hidden and delayed nodes are always passive.
	// Arbiter cannot be primary because it is not a data-bearing member (non-writable)
	// but it is not passive (has 1 election vote).
	Passive bool `bson:"psv"`

	// Arbiter is true for argiter node.
	Arbiter bool `bson:"arb"`

	// DelaySecs is the node configured replication delay (lag).
	DelaySecs int32 `bson:"delay"`

	// Replication lag for mongod.
	ReplicationLag int `bson:"repl_lag"`

	// AgentVer has the PBM Agent version (looks like `v2.3.4`)
	AgentVer string `bson:"v"`

	// MongoVer is the mongod version (looks like `v7.0.0`)
	MongoVer string `bson:"mv"`

	// PerconaVer is the PSMDB version (looks like `v7.0.0-1`).
	//
	// Empty for non-PSMDB (e.i MongoDB CE).
	PerconaVer string `bson:"pv,omitempty"`

	// PBMStatus is the agent status.
	PBMStatus SubsysStatus `bson:"pbms"`

	// NodeStatus is the mongod/connection status.
	NodeStatus SubsysStatus `bson:"nodes"`

	// StorageStatus is the remote storage status.
	StorageStatus SubsysStatus `bson:"stors"`

	// Heartbeat is agent's last seen cluster time.
	Heartbeat primitive.Timestamp `bson:"hb"`

	// Err can be any error.
	Err string `bson:"e"`
}

// SubsysStatus is generic status.
type SubsysStatus struct {
	// OK is false if there is an error. Otherwise true.
	OK bool `bson:"ok"`

	// Err is error string.
	Err string `bson:"e"`
}

// IsStale returns true if agent's heartbeat is steal for the give `t` cluster time.
func (s *AgentStat) IsStale(t primitive.Timestamp) bool {
	return s.Heartbeat.T+defs.StaleFrameSec < t.T
}

func (s *AgentStat) OK() (bool, []error) {
	var errs []error
	ok := true
	if !s.PBMStatus.OK {
		ok = false
		errs = append(errs, errors.Errorf("PBM connection: %s", s.PBMStatus.Err))
	}
	if !s.NodeStatus.OK {
		ok = false
		errs = append(errs, errors.Errorf("node connection: %s", s.NodeStatus.Err))
	}
	if !s.StorageStatus.OK {
		ok = false
		errs = append(errs, errors.Errorf("storage: %s", s.StorageStatus.Err))
	}

	return ok, errs
}

func (s *AgentStat) MongoVersion() version.MongoVersion {
	v := version.MongoVersion{
		PSMDBVersion:  s.PerconaVer,
		VersionString: s.MongoVer,
	}

	vs := semver.Canonical("v" + s.MongoVer)[1:]
	vs = strings.SplitN(vs, "-", 2)[0]
	for _, a := range strings.Split(vs, ".")[:3] {
		n, _ := strconv.Atoi(a)
		v.Version = append(v.Version, n)
	}

	return v
}

func SetAgentStatus(ctx context.Context, m connect.Client, stat *AgentStat) error {
	_, err := m.AgentsStatusCollection().ReplaceOne(
		ctx,
		bson.D{{"n", stat.Node}, {"rs", stat.RS}},
		stat,
		options.Replace().SetUpsert(true),
	)
	return errors.Wrap(err, "write into db")
}

func RemoveAgentStatus(ctx context.Context, m connect.Client, stat AgentStat) error {
	_, err := m.AgentsStatusCollection().
		DeleteOne(ctx, bson.D{{"n", stat.Node}, {"rs", stat.RS}})
	return errors.Wrap(err, "query")
}

// GetAgentStatus returns agent status by given node and rs
// it's up to user how to handle ErrNoDocuments
func GetAgentStatus(ctx context.Context, m connect.Client, rs, node string) (AgentStat, error) {
	res := m.AgentsStatusCollection().FindOne(
		ctx,
		bson.D{{"n", node}, {"rs", rs}},
	)
	if res.Err() != nil {
		return AgentStat{}, errors.Wrap(res.Err(), "query mongo")
	}

	var s AgentStat
	err := res.Decode(&s)
	return s, errors.Wrap(err, "decode")
}

// AgentStatusGC cleans up stale agent statuses
func AgentStatusGC(ctx context.Context, m connect.Client) error {
	ct, err := GetClusterTime(ctx, m)
	if err != nil {
		return errors.Wrap(err, "get cluster time")
	}
	// 30 secs is the connection time out for mongo. So if there are some connection issues the agent checker
	// may stuck for 30 sec on ping (trying to connect), it's HB became stale and it would be collected.
	// Which would lead to the false clamin "not found" in the status output. So stale range should at least 30 sec
	// (+5 just in case).
	// XXX: stalesec is const 15 secs which resolves to 35 secs
	stalesec := defs.AgentsStatCheckRange.Seconds() * 3
	if stalesec < 35 {
		stalesec = 35
	}
	ct.T -= uint32(stalesec)
	_, err = m.AgentsStatusCollection().DeleteMany(
		ctx,
		bson.M{"hb": bson.M{"$lt": ct}},
	)

	return errors.Wrap(err, "delete")
}

// ListAgentStatuses returns list of registered agents
func ListAgentStatuses(ctx context.Context, m connect.Client) ([]AgentStat, error) {
	if err := AgentStatusGC(ctx, m); err != nil {
		return nil, errors.Wrap(err, "remove stale statuses")
	}

	return ListAgents(ctx, m)
}

// ListSteadyAgents returns agents which are in steady state for backup or PITR.
func ListSteadyAgents(ctx context.Context, m connect.Client) ([]AgentStat, error) {
	agents, err := ListAgentStatuses(ctx, m)
	if err != nil {
		return nil, errors.Wrap(err, "listing agents")
	}
	steadyAgents := []AgentStat{}
	for _, a := range agents {
		if a.State != defs.NodeStatePrimary &&
			a.State != defs.NodeStateSecondary {
			continue
		}
		if a.Arbiter || a.DelaySecs > 0 {
			continue
		}
		if a.ReplicationLag >= defs.MaxReplicationLagTimeSec {
			continue
		}

		steadyAgents = append(steadyAgents, a)
	}

	return steadyAgents, nil
}

func ListAgents(ctx context.Context, m connect.Client) ([]AgentStat, error) {
	cur, err := m.AgentsStatusCollection().Find(ctx, bson.D{})
	if err != nil {
		return nil, errors.Wrap(err, "query")
	}

	var agents []AgentStat
	err = cur.All(ctx, &agents)
	return agents, errors.Wrap(err, "decode")
}
