package membergroup

import (
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

// Set is the ordered, validated set of groups for one replica set.
type Set struct {
	policy      Policy
	rsName      string
	groups      []Group
	byName      map[string]int
	byComponent map[string]int
}

func (s *Set) GetPolicy() Policy      { return s.policy }
func (s *Set) GetReplsetName() string { return s.rsName }
func (s *Set) Len() int               { return len(s.groups) }

func (s *Set) GetAll() []Group { return s.groups }

func (s *Set) GetNames() []string {
	names := make([]string, 0, len(s.groups))
	for i := range s.groups {
		names = append(names, s.groups[i].Name)
	}
	return names
}

func (s *Set) GetStatefulSetNames() []string {
	names := make([]string, 0, len(s.groups))
	for i := range s.groups {
		names = append(names, s.groups[i].STSName)
	}
	return names
}

func (s *Set) GetByName(name string) (Group, bool) {
	i, ok := s.byName[name]
	if !ok {
		return Group{}, false
	}
	return s.groups[i], true
}

func (s *Set) GetByComponent(component string) (Group, bool) {
	i, ok := s.byComponent[component]
	if !ok {
		return Group{}, false
	}
	return s.groups[i], true
}

func (s *Set) GetByLabels(ls map[string]string) (Group, bool) {
	component, ok := ls[naming.LabelKubernetesComponent]
	if !ok || component == "" {
		return Group{}, false
	}
	return s.GetByComponent(component)
}

func (s *Set) GetDataBearing() []Group {
	out := make([]Group, 0, len(s.groups))
	for i := range s.groups {
		if s.groups[i].DataBearing {
			out = append(out, s.groups[i])
		}
	}
	return out
}

// PrimaryEligible returns the groups whose configuration permits an election
// and that have at least one member.
// To be used only as a filter.
func (s *Set) GetPrimaryEligible() []Group {
	out := make([]Group, 0, len(s.groups))
	for i := range s.groups {
		if s.groups[i].PrimaryEligible && s.groups[i].Replicas > 0 {
			out = append(out, s.groups[i])
		}
	}
	return out
}

func (s *Set) GetTotalMemberCount() int32 {
	var n int32
	for i := range s.groups {
		n += s.groups[i].Replicas
	}
	return n
}

func (s *Set) GetVoterCount() int32 {
	var n int32
	for i := range s.groups {
		if s.groups[i].Member.Votes > 0 {
			n += s.groups[i].Replicas
		}
	}
	return n
}

func (s *Set) GetDataBearingVoterCount() int32 {
	var n int32
	for i := range s.groups {
		g := &s.groups[i]
		if g.DataBearing && g.Member.Votes > 0 {
			n += g.Replicas
		}
	}
	return n
}

func (s *Set) GetArbiterMemberCount() int32 {
	var n int32
	for i := range s.groups {
		if s.groups[i].Member.ArbiterOnly {
			n += s.groups[i].Replicas
		}
	}
	return n
}

func (s *Set) GetNonVotingMemberCount() int32 {
	var n int32
	for i := range s.groups {
		if s.groups[i].Member.Votes == 0 && !s.groups[i].Member.ArbiterOnly {
			n += s.groups[i].Replicas
		}
	}
	return n
}

func (s *Set) GetTotalMemberCountWithExternal(rs *api.ReplsetSpec) int32 {
	return s.GetTotalMemberCount() + int32(len(rs.ExternalNodes))
}
