package cli

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/sync/errgroup"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/prio"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/sdk"
)

type LostAgentError struct {
	heartbeat primitive.Timestamp
}

func (e LostAgentError) Error() string {
	return fmt.Sprintf("lost agent, last heartbeat: %v", e.heartbeat.T)
}

type RSRole = topo.NodeRole

const (
	RolePrimary   = topo.RolePrimary
	RoleSecondary = topo.RoleSecondary
	RoleArbiter   = topo.RoleArbiter
	RoleHidden    = topo.RoleHidden
	RoleDelayed   = topo.RoleDelayed
)

type Node struct {
	Host     string
	Ver      string
	Role     RSRole
	PrioPITR float64
	PrioBcp  float64
	OK       bool
	Errs     []error
}

func (n Node) IsAgentLost() bool {
	if len(n.Errs) == 0 {
		return false
	}

	lostErr := LostAgentError{}
	for _, err := range n.Errs {
		if errors.As(err, &lostErr) {
			return true
		}
	}

	return false
}

func ClusterStatus(
	ctx context.Context,
	pbm *sdk.Client,
	confGetter RSConfGetter,
) (map[string][]Node, error) {
	cfg, err := pbm.GetConfig(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "get config")
	}
	clusterMembers, err := sdk.ClusterMembers(ctx, pbm)
	if err != nil {
		return nil, errors.Wrap(err, "get agent statuses")
	}
	agentStatuses, err := sdk.AgentStatuses(ctx, pbm)
	if err != nil {
		return nil, errors.Wrap(err, "get cluster members")
	}
	clusterTime, err := sdk.ClusterTime(ctx, pbm)
	if err != nil {
		return nil, errors.Wrap(err, "read cluster time")
	}

	agentMap := make(map[topo.ReplsetName]map[string]*sdk.AgentStatus, len(clusterMembers))
	for i := range agentStatuses {
		agent := &agentStatuses[i]
		rs, ok := agentMap[agent.RS]
		if !ok {
			rs = make(map[string]*topo.AgentStat)
			agentMap[agent.RS] = rs
		}

		rs[agent.Node] = agent
		agentMap[agent.RS] = rs
	}

	eg, ctx := errgroup.WithContext(ctx)
	m := sync.Mutex{}

	pbmCluster := make(map[string][]Node)
	for _, c := range clusterMembers {
		eg.Go(func() error {
			rsConf, err := confGetter.Get(ctx, c.Host)
			if err != nil {
				return errors.Wrapf(err, "get replset status for `%s`", c.RS)
			}

			nodes := make([]Node, len(rsConf.Members))
			for i, member := range rsConf.Members {
				node := &nodes[i]
				node.Host = member.Host

				rsAgents := agentMap[c.RS]
				if rsAgents == nil {
					node.Role = member.Role()
					continue
				}
				agent := rsAgents[member.Host]
				if agent == nil {
					node.Role = member.Role()
					continue
				}

				node.Ver = "v" + agent.AgentVer
				node.PrioBcp = prio.CalcPriorityForAgent(agent, cfg.Backup.Priority, nil)
				node.PrioPITR = prio.CalcPriorityForAgent(agent, cfg.PITR.Priority, nil)

				switch {
				case agent.State == 1: // agent.StateStr == "PRIMARY"
					node.Role = RolePrimary
				case agent.State == 7: // agent.StateStr == "ARBITER"
					node.Role = RoleArbiter
				case agent.State == 2: // agent.StateStr == "SECONDARY"
					if agent.DelaySecs != 0 {
						node.Role = RoleDelayed
					} else if agent.Hidden {
						node.Role = RoleHidden
					} else {
						node.Role = RoleSecondary
					}
				default:
					// unexpected state. show actual state
					node.Role = RSRole(agent.StateStr)
				}

				if agent.IsStale(clusterTime) {
					node.Errs = []error{LostAgentError{agent.Heartbeat}}
					continue
				}

				node.OK, node.Errs = agent.OK()
			}

			m.Lock()
			pbmCluster[c.RS] = nodes
			m.Unlock()
			return nil
		})
	}

	err = eg.Wait()
	return pbmCluster, err
}

type RSConfGetter string

func (g RSConfGetter) Get(ctx context.Context, host string) (*topo.RSConfig, error) {
	rsName, host, ok := strings.Cut(host, "/")
	if !ok {
		host = rsName
	}

	if !strings.HasPrefix(string(g), "mongodb://") {
		g = "mongodb://" + g
	}
	curi, err := url.Parse(string(g))
	if err != nil {
		return nil, errors.Wrapf(err, "parse mongo-uri '%s'", g)
	}

	// Preserving the `replicaSet` parameter will cause an error
	// while connecting to the ConfigServer (mismatched replicaset names)
	query := curi.Query()
	query.Del("replicaSet")
	curi.RawQuery = query.Encode()
	curi.Host = host

	conn, err := connect.MongoConnect(ctx, curi.String(), connect.AppName("pbm-sdk"))
	if err != nil {
		return nil, errors.Wrap(err, "connect")
	}
	defer func() { _ = conn.Disconnect(context.Background()) }()

	return topo.GetReplSetConfig(ctx, conn)
}
