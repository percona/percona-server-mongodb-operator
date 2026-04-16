package prio

import (
	"reflect"
	"testing"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
)

func TestCalcNodesPriority(t *testing.T) {
	t.Run("implicit priorities - rs", func(t *testing.T) {
		testCases := []struct {
			desc   string
			agents []topo.AgentStat
			res    [][]string
		}{
			{
				desc: "implicit priorities for PSS",
				agents: []topo.AgentStat{
					newP("rs0", "rs01"),
					newS("rs0", "rs02"),
					newS("rs0", "rs03"),
				},
				res: [][]string{
					{"rs02", "rs03"},
					{"rs01"},
				},
			},
			{
				desc: "implicit priorities for PSH",
				agents: []topo.AgentStat{
					newP("rs0", "rs01"),
					newS("rs0", "rs02"),
					newH("rs0", "rs03"),
				},
				res: [][]string{
					{"rs03"},
					{"rs02"},
					{"rs01"},
				},
			},
			{
				desc: "implicit priorities for PSA",
				agents: []topo.AgentStat{
					newP("rs0", "rs01"),
					newS("rs0", "rs02"),
					newA("rs0", "rs03"),
				},
				res: [][]string{
					{"rs02"},
					{"rs01"},
				},
			},
			{
				desc: "5 members mix",
				agents: []topo.AgentStat{
					newS("rs0", "rs01"),
					newH("rs0", "rs02"),
					newP("rs0", "rs03"),
					newA("rs0", "rs04"),
					newH("rs0", "rs05"),
				},
				res: [][]string{
					{"rs02", "rs05"},
					{"rs01"},
					{"rs03"},
				},
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				np := CalcNodesPriority(nil, nil, tC.agents)

				prioByScore := np.RS(tC.agents[0].RS)

				if !reflect.DeepEqual(prioByScore, tC.res) {
					t.Fatalf("wrong nodes priority calculation: want=%v, got=%v", tC.res, prioByScore)
				}
			})
		}
	})

	t.Run("implicit priorities - sharded cluster", func(t *testing.T) {
		testCases := []struct {
			desc   string
			agents []topo.AgentStat
			resCfg [][]string
			resRS0 [][]string
			resRS1 [][]string
		}{
			{
				desc: "implicit priorities for PSS",
				agents: []topo.AgentStat{
					newP("rs0", "rs01"),
					newS("rs0", "rs02"),
					newS("rs0", "rs03"),
					newS("rs1", "rs11"),
					newP("rs1", "rs12"),
					newS("rs1", "rs13"),
					newS("cfg", "cfg1"),
					newS("cfg", "cfg2"),
					newP("cfg", "cfg3"),
				},
				resCfg: [][]string{
					{"cfg1", "cfg2"},
					{"cfg3"},
				},
				resRS0: [][]string{
					{"rs02", "rs03"},
					{"rs01"},
				},
				resRS1: [][]string{
					{"rs11", "rs13"},
					{"rs12"},
				},
			},
			{
				desc: "implicit priorities for sharded mix",
				agents: []topo.AgentStat{
					newS("cfg", "cfg1"),
					newP("cfg", "cfg2"),
					newS("cfg", "cfg3"),
					newS("rs0", "rs01"),
					newP("rs0", "rs02"),
					newA("rs0", "rs03"),
					newP("rs1", "rs11"),
					newH("rs1", "rs12"),
					newS("rs1", "rs13"),
				},
				resCfg: [][]string{
					{"cfg1", "cfg3"},
					{"cfg2"},
				},
				resRS0: [][]string{
					{"rs01"},
					{"rs02"},
				},
				resRS1: [][]string{
					{"rs12"},
					{"rs13"},
					{"rs11"},
				},
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				np := CalcNodesPriority(nil, nil, tC.agents)

				prioByScoreCfg := np.RS("cfg")
				prioByScoreRs0 := np.RS("rs0")
				prioByScoreRs1 := np.RS("rs1")

				if !reflect.DeepEqual(prioByScoreCfg, tC.resCfg) {
					t.Fatalf("wrong nodes priority calculation for config cluster: want=%v, got=%v", tC.resCfg, prioByScoreCfg)
				}
				if !reflect.DeepEqual(prioByScoreRs0, tC.resRS0) {
					t.Fatalf("wrong nodes priority calculation for rs1 cluster: want=%v, got=%v", tC.resRS0, prioByScoreRs0)
				}
				if !reflect.DeepEqual(prioByScoreRs1, tC.resRS1) {
					t.Fatalf("wrong nodes priority calculation for rs2 cluster: want=%v, got=%v", tC.resRS1, prioByScoreRs1)
				}
			})
		}
	})

	t.Run("explicit priorities - rs", func(t *testing.T) {
		testCases := []struct {
			desc    string
			agents  []topo.AgentStat
			expPrio config.Priority
			res     [][]string
		}{
			{
				desc: "all priorities are different",
				agents: []topo.AgentStat{
					newP("rs0", "rs01"),
					newS("rs0", "rs02"),
					newS("rs0", "rs03"),
				},
				expPrio: config.Priority{
					"rs01": 2.0,
					"rs02": 3.0,
					"rs03": 1.0,
				},
				res: [][]string{
					{"rs02"},
					{"rs01"},
					{"rs03"},
				},
			},
			{
				desc: "5 members, 3 different priority groups",
				agents: []topo.AgentStat{
					newP("rs0", "rs01"),
					newS("rs0", "rs02"),
					newS("rs0", "rs03"),
					newS("rs0", "rs04"),
					newS("rs0", "rs05"),
				},
				expPrio: config.Priority{
					"rs01": 2.0,
					"rs02": 3.0,
					"rs03": 1.0,
					"rs04": 1.0,
					"rs05": 3.0,
				},
				res: [][]string{
					{"rs02", "rs05"},
					{"rs01"},
					{"rs03", "rs04"},
				},
			},
			{
				desc: "default priorities",
				agents: []topo.AgentStat{
					newP("rs0", "rs01"),
					newS("rs0", "rs02"),
					newS("rs0", "rs03"),
				},
				expPrio: config.Priority{
					"rs01": 0.5,
				},
				res: [][]string{
					{"rs02", "rs03"},
					{"rs01"},
				},
			},
			{
				desc: "priorities are not defined -> implicit are applied",
				agents: []topo.AgentStat{
					newP("rs0", "rs01"),
					newS("rs0", "rs02"),
					newS("rs0", "rs03"),
				},
				expPrio: nil,
				res: [][]string{
					{"rs02", "rs03"},
					{"rs01"},
				},
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				np := CalcNodesPriority(nil, tC.expPrio, tC.agents)

				prioByScore := np.RS(tC.agents[0].RS)

				if !reflect.DeepEqual(prioByScore, tC.res) {
					t.Fatalf("wrong nodes priority calculation: want=%v, got=%v", tC.res, prioByScore)
				}
			})
		}
	})

	t.Run("explicit priorities - sharded cluster", func(t *testing.T) {
		testCases := []struct {
			desc    string
			agents  []topo.AgentStat
			expPrio config.Priority
			res     [][]string
			resCfg  [][]string
			resRS0  [][]string
			resRS1  [][]string
		}{
			{
				desc: "all priorities are different",
				agents: []topo.AgentStat{
					newP("rs0", "rs01"),
					newS("rs0", "rs02"),
					newS("rs0", "rs03"),
					newS("rs1", "rs11"),
					newP("rs1", "rs12"),
					newS("rs1", "rs13"),
					newS("cfg", "cfg1"),
					newS("cfg", "cfg2"),
					newP("cfg", "cfg3"),
				},
				expPrio: config.Priority{
					"rs01": 2.0,
					"rs02": 3.0,
					"rs03": 1.0,
					"rs11": 2.0,
					"rs12": 3.0,
					"rs13": 1.0,
					"cfg2": 2.0,
				},
				resCfg: [][]string{
					{"cfg2"},
					{"cfg1", "cfg3"},
				},
				resRS0: [][]string{
					{"rs02"},
					{"rs01"},
					{"rs03"},
				},
				resRS1: [][]string{
					{"rs12"},
					{"rs11"},
					{"rs13"},
				},
			},
			{
				desc: "only primary is down prioritized",
				agents: []topo.AgentStat{
					newP("rs0", "rs01"),
					newS("rs0", "rs02"),
					newS("rs0", "rs03"),
					newS("rs1", "rs11"),
					newP("rs1", "rs12"),
					newS("rs1", "rs13"),
					newS("cfg", "cfg1"),
					newS("cfg", "cfg2"),
					newP("cfg", "cfg3"),
				},
				expPrio: config.Priority{
					"rs01": 0.5,
					"rs12": 0.5,
					"cfg3": 0.5,
				},
				resCfg: [][]string{
					{"cfg1", "cfg2"},
					{"cfg3"},
				},
				resRS0: [][]string{
					{"rs02", "rs03"},
					{"rs01"},
				},
				resRS1: [][]string{
					{"rs11", "rs13"},
					{"rs12"},
				},
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				np := CalcNodesPriority(nil, tC.expPrio, tC.agents)

				prioByScoreCfg := np.RS("cfg")
				prioByScoreRs0 := np.RS("rs0")
				prioByScoreRs1 := np.RS("rs1")

				if !reflect.DeepEqual(prioByScoreCfg, tC.resCfg) {
					t.Fatalf("wrong nodes priority calculation for config cluster: want=%v, got=%v", tC.resCfg, prioByScoreCfg)
				}
				if !reflect.DeepEqual(prioByScoreRs0, tC.resRS0) {
					t.Fatalf("wrong nodes priority calculation for rs1 cluster: want=%v, got=%v", tC.resRS0, prioByScoreRs0)
				}
				if !reflect.DeepEqual(prioByScoreRs1, tC.resRS1) {
					t.Fatalf("wrong nodes priority calculation for rs2 cluster: want=%v, got=%v", tC.resRS1, prioByScoreRs1)
				}
			})
		}
	})

	t.Run("coeficients", func(t *testing.T) {
		agents := []topo.AgentStat{
			newP("rs0", "rs01"),
			newS("rs0", "rs02"),
			newS("rs0", "rs03"),
		}
		res := [][]string{
			{"rs03"},
			{"rs02"},
			{"rs01"},
		}
		c := map[string]float64{
			"rs03": 3.0,
		}

		np := CalcNodesPriority(c, nil, agents)

		prioByScore := np.RS(agents[0].RS)
		if !reflect.DeepEqual(prioByScore, res) {
			t.Fatalf("wrong nodes priority calculation: want=%v, got=%v", res, prioByScore)
		}
	})
}

func TestCalcPriorityForNode(t *testing.T) {
	t.Run("for primary", func(t *testing.T) {
		nodeInfo := &topo.NodeInfo{
			IsPrimary: true,
		}

		p := CalcPriorityForNode(nodeInfo)
		if p != scoreForPrimary {
			t.Errorf("wrong priority for primary: want=%v, got=%v", scoreForPrimary, p)
		}
	})

	t.Run("for secondary", func(t *testing.T) {
		nodeInfo := &topo.NodeInfo{
			Secondary: true,
		}

		p := CalcPriorityForNode(nodeInfo)
		if p != defaultScore {
			t.Errorf("wrong priority for secondary: want=%v, got=%v", defaultScore, p)
		}
	})

	t.Run("for hidden", func(t *testing.T) {
		nodeInfo := &topo.NodeInfo{
			Hidden:    true,
			Secondary: true, // hidden is also secondary
		}

		p := CalcPriorityForNode(nodeInfo)
		if p != scoreForHidden {
			t.Errorf("wrong priority for hidden: want=%v, got=%v", scoreForHidden, p)
		}
	})

	t.Run("for delayed (slaveDelayed)", func(t *testing.T) {
		nodeInfo := &topo.NodeInfo{
			Secondary:         true,
			SecondaryDelayOld: 5,
		}

		p := CalcPriorityForNode(nodeInfo)
		if p != scoreForExcluded {
			t.Errorf("wrong priority for hidden: want=%v, got=%v", scoreForExcluded, p)
		}
	})

	t.Run("for delayed", func(t *testing.T) {
		nodeInfo := &topo.NodeInfo{
			Secondary:          true,
			SecondaryDelaySecs: 1,
		}

		p := CalcPriorityForNode(nodeInfo)
		if p != scoreForExcluded {
			t.Errorf("wrong priority for hidden: want=%v, got=%v", scoreForExcluded, p)
		}
	})

	t.Run("for hidden & delayed", func(t *testing.T) {
		nodeInfo := &topo.NodeInfo{
			Secondary:          true,
			SecondaryDelaySecs: 3600,
			Hidden:             true,
		}

		p := CalcPriorityForNode(nodeInfo)
		if p != scoreForExcluded {
			t.Errorf("wrong priority for hidden: want=%v, got=%v", scoreForExcluded, p)
		}
	})
}

func TestImplicitPrioCalc(t *testing.T) {
	t.Run("for primary", func(t *testing.T) {
		agentStat := &topo.AgentStat{
			State: defs.NodeStatePrimary,
		}

		p := implicitPrioCalc(agentStat, nil)
		if p != scoreForPrimary {
			t.Errorf("wrong priority for primary: want=%v, got=%v", scoreForPrimary, p)
		}
	})

	t.Run("for secondary", func(t *testing.T) {
		agentStat := &topo.AgentStat{
			State: defs.NodeStateSecondary,
		}

		p := implicitPrioCalc(agentStat, nil)

		if p != defaultScore {
			t.Errorf("wrong priority for secondary: want=%v, got=%v", defaultScore, p)
		}
	})

	t.Run("for hidden", func(t *testing.T) {
		agentStat := &topo.AgentStat{
			State:  defs.NodeStateSecondary,
			Hidden: true,
		}

		p := implicitPrioCalc(agentStat, nil)

		if p != scoreForHidden {
			t.Errorf("wrong priority for hidden: want=%v, got=%v", scoreForHidden, p)
		}
	})

	t.Run("for delayed", func(t *testing.T) {
		agentStat := &topo.AgentStat{
			State:     defs.NodeStateSecondary,
			DelaySecs: 3600,
		}

		p := implicitPrioCalc(agentStat, nil)

		if p != scoreForExcluded {
			t.Errorf("wrong priority for hidden: want=%v, got=%v", scoreForExcluded, p)
		}
	})

	t.Run("for hidden & delayed", func(t *testing.T) {
		agentStat := &topo.AgentStat{
			State:     defs.NodeStateSecondary,
			DelaySecs: 3600,
			Hidden:    true,
		}

		p := implicitPrioCalc(agentStat, nil)

		if p != scoreForExcluded {
			t.Errorf("wrong priority for hidden: want=%v, got=%v", scoreForExcluded, p)
		}
	})
}

func newP(rs, node string) topo.AgentStat {
	return newAgent(rs, node, defs.NodeStatePrimary, false)
}

func newS(rs, node string) topo.AgentStat {
	return newAgent(rs, node, defs.NodeStateSecondary, false)
}

func newH(rs, node string) topo.AgentStat {
	return newAgent(rs, node, defs.NodeStateSecondary, true)
}

func newA(rs, node string) topo.AgentStat {
	return newAgent(rs, node, defs.NodeStateArbiter, false)
}

func newAgent(rs, node string, state defs.NodeState, isHidden bool) topo.AgentStat {
	return topo.AgentStat{
		Node:    node,
		RS:      rs,
		State:   state,
		Hidden:  isHidden,
		Arbiter: state == defs.NodeStateArbiter,
		PBMStatus: topo.SubsysStatus{
			OK: true,
		},
		NodeStatus: topo.SubsysStatus{
			OK: state == defs.NodeStatePrimary || state == defs.NodeStateSecondary,
		},
		StorageStatus: topo.SubsysStatus{
			OK: true,
		},
	}
}
