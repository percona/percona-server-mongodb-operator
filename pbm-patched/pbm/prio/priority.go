package prio

import (
	"sort"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
)

const (
	defaultScore     = 1.0
	scoreForPrimary  = defaultScore / 2
	scoreForHidden   = defaultScore * 2
	scoreForExcluded = 0
)

// NodesPriority groups nodes by priority according to
// provided scores. Basically nodes are grouped and sorted by
// descending order by score
type NodesPriority struct {
	m map[string]nodeScores
}

func NewNodesPriority() *NodesPriority {
	return &NodesPriority{make(map[string]nodeScores)}
}

// Add node with its score
func (n *NodesPriority) Add(rs, node string, sc float64) {
	s, ok := n.m[rs]
	if !ok {
		s = nodeScores{m: make(map[float64][]string)}
	}
	s.add(node, sc)
	n.m[rs] = s
}

// RS returns nodes `group and sort desc by score` for given replset
func (n *NodesPriority) RS(rs string) [][]string {
	return n.m[rs].list()
}

// CalcNodesPriority calculates and returns list nodes grouped by
// backup/pitr preferences in descended order.
// First are nodes with the highest priority.
// Custom coefficients might be passed. These will be ignored though
// if the config is set.
func CalcNodesPriority(
	c map[string]float64,
	cfgPrio config.Priority,
	agents []topo.AgentStat,
) *NodesPriority {
	scores := NewNodesPriority()

	for _, a := range agents {
		if ok, _ := a.OK(); !ok {
			continue
		}

		scores.Add(a.RS, a.Node, CalcPriorityForAgent(&a, cfgPrio, c))
	}

	return scores
}

type nodeScores struct {
	idx []float64
	m   map[float64][]string
}

func (s *nodeScores) add(node string, sc float64) {
	nodes, ok := s.m[sc]
	if !ok {
		s.idx = append(s.idx, sc)
	}
	s.m[sc] = append(nodes, node)
}

func (s nodeScores) list() [][]string {
	ret := make([][]string, len(s.idx))
	sort.Sort(sort.Reverse(sort.Float64Slice(s.idx)))

	for i := range ret {
		ret[i] = s.m[s.idx[i]]
	}

	return ret
}

// CalcPriorityForAgent calculates priority for the specified agent.
func CalcPriorityForAgent(
	agent *topo.AgentStat,
	cfgPrio config.Priority,
	coeffRules map[string]float64,
) float64 {
	if len(cfgPrio) > 0 {
		// apply config level priorities
		return explicitPrioCalc(agent, cfgPrio)
	}

	// if config level priorities (cfgPrio) aren't set,
	// apply priorities based on topology rules
	return implicitPrioCalc(agent, coeffRules)
}

// CalcPriorityForNode returns implicit priority based on node info.
func CalcPriorityForNode(node *topo.NodeInfo) float64 {
	if node.IsPrimary {
		return scoreForPrimary
	} else if node.IsDelayed() {
		return scoreForExcluded
	} else if node.Hidden {
		return scoreForHidden
	}

	return defaultScore
}

// implicitPrioCalc provides priority calculation based on topology rules.
// Instead of using explicitly specified priority numbers, topology rules are
// applied for primary, secondary and hidden member.
func implicitPrioCalc(a *topo.AgentStat, rule map[string]float64) float64 {
	if coeff, ok := rule[a.Node]; ok && rule != nil {
		return defaultScore * coeff
	} else if a.State == defs.NodeStatePrimary {
		return scoreForPrimary
	} else if a.DelaySecs > 0 {
		return scoreForExcluded
	} else if a.Hidden {
		return scoreForHidden
	}
	return defaultScore
}

// explicitPrioCalc uses priority numbers from configuration to calculate
// priority for the specified agent.
// In case when priority is not specified, default one is used instead.
func explicitPrioCalc(a *topo.AgentStat, rule map[string]float64) float64 {
	sc, ok := rule[a.Node]
	if !ok || sc < 0 {
		return defaultScore
	}

	return sc
}
