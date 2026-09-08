package membergroup

import (
	"sort"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

// Resolve maps a defaulted (cr, rs) pair to its member groups.
func Resolve(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) (*Set, error) {
	if cr == nil {
		return nil, errors.New("resolve member groups: cr is nil")
	}
	if rs == nil {
		return nil, errors.New("resolve member groups: replset spec is nil")
	}

	set := &Set{rsName: rs.Name}

	var err error
	if rs.InstanceMode() {
		set.groups, set.policy, err = resolveInstances(cr, rs)
	} else {
		set.groups, set.policy, err = resolveLegacy(cr, rs)
	}
	if err != nil {
		return nil, errors.Wrapf(err, "resolve member groups for replset %s", rs.Name)
	}

	set.byName = make(map[string]int, len(set.groups))
	set.byComponent = make(map[string]int, len(set.groups))
	for i := range set.groups {
		g := &set.groups[i]
		if _, dup := set.byName[g.Name]; dup {
			return nil, errors.Errorf("replset %s: duplicate group name %q", rs.Name, g.Name)
		}
		if _, dup := set.byComponent[g.Component]; dup {
			return nil, errors.Errorf("replset %s: duplicate component label %q", rs.Name, g.Component)
		}
		set.byName[g.Name] = i
		set.byComponent[g.Component] = i
	}

	return set, nil
}

// resolveInstances maps instances[] entries to groups, sorted by name so that
// reordering the list cannot change iteration order, member IDs or rollout
// sequence.
func resolveInstances(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) ([]Group, Policy, error) {
	policy := PolicyExplicit
	if rs.UseLegacyVotePolicy() {
		policy = PolicyImplicit
	}

	groups := make([]Group, 0, len(rs.Instances))
	for i := range rs.Instances {
		inst := &rs.Instances[i]

		g := Group{
			Name:                     inst.Name,
			Replicas:                 inst.Replicas,
			MultiAZ:                  *inst.MultiAZ.DeepCopy(),
			VolumeSpec:               inst.VolumeSpec.DeepCopy(),
			Configuration:            rs.Configuration,
			LivenessProbe:            inst.LivenessProbe.DeepCopy(),
			ReadinessProbe:           inst.ReadinessProbe.DeepCopy(),
			PodSecurityContext:       inst.PodSecurityContext.DeepCopy(),
			ContainerSecurityContext: inst.ContainerSecurityContext.DeepCopy(),
			Env:                      copySlice(inst.Env),
			EnvFrom:                  copySlice(inst.EnvFrom),
			Member:                   resolveMemberConfig(inst, policy),
			DataBearing:              inst.IsDataBearing(),
			PrimaryEligible:          inst.IsPrimaryEligible(),
			Source: SourceRef{
				ReplsetName:  rs.Name,
				InstanceName: inst.Name,
			},
		}

		if !g.DataBearing {
			g.VolumeSpec = nil
		}

		finishIdentity(cr, rs, &g)
		groups = append(groups, g)
	}

	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })

	return groups, policy, nil
}

const (
	legacyDefaultVotes    = 1
	legacyDefaultPriority = 2
)

func resolveMemberConfig(inst *api.InstanceSpec, policy Policy) MemberConfig {
	if policy == PolicyImplicit {
		switch inst.Name {
		case api.ReservedGroupArbiter:
			return MemberConfig{ArbiterOnly: true, Votes: legacyDefaultVotes, Priority: 0}
		case api.ReservedGroupNonVoting:
			return MemberConfig{
				Votes: 0, Priority: 0,
				Tags: map[string]string{naming.ComponentNonVoting: "true"},
			}
		case api.ReservedGroupHidden:
			return MemberConfig{
				Hidden: true, Votes: legacyDefaultVotes, Priority: 0,
				Tags: map[string]string{naming.ComponentHidden: "true"},
			}
		default: // or api.ReservedGroupMongod
			return MemberConfig{Votes: legacyDefaultVotes, Priority: legacyDefaultPriority}
		}
	}

	mc := MemberConfig{
		Priority:    int(inst.ResolvedPriority()),
		Votes:       int(inst.GetVotes()),
		Hidden:      inst.IsHidden(),
		ArbiterOnly: inst.IsArbiterOnly(),
		Tags:        util.MapCopy(inst.GetTags()),
	}
	if mc.ArbiterOnly {
		mc.Tags = nil
	}
	return mc
}

func resolveLegacy(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) ([]Group, Policy, error) {
	groups := make([]Group, 0, 4)

	mongod := Group{
		Name:                     naming.GroupMongod,
		Replicas:                 rs.Size,
		MultiAZ:                  *rs.MultiAZ.DeepCopy(),
		VolumeSpec:               rs.VolumeSpec.DeepCopy(),
		Configuration:            rs.Configuration,
		LivenessProbe:            rs.LivenessProbe.DeepCopy(),
		ReadinessProbe:           rs.ReadinessProbe.DeepCopy(),
		PodSecurityContext:       rs.PodSecurityContext.DeepCopy(),
		ContainerSecurityContext: rs.ContainerSecurityContext.DeepCopy(),
		Env:                      copySlice(rs.Env),
		EnvFrom:                  copySlice(rs.EnvFrom),
		Member: MemberConfig{
			Priority: legacyDefaultPriority,
			Votes:    legacyDefaultVotes,
		},
		DataBearing:     true,
		PrimaryEligible: true,
		Source:          SourceRef{ReplsetName: rs.Name},
	}
	groups = append(groups, mongod)

	if rs.Arbiter.Enabled {
		groups = append(groups, Group{
			Name:     naming.GroupArbiter,
			Replicas: rs.Arbiter.Size,
			MultiAZ:  *rs.Arbiter.MultiAZ.DeepCopy(),
			// Arbiters hold no data: an emptyDir is injected by the workload
			// builder, so no VolumeSpec is resolved here.
			VolumeSpec: nil,
			// Arbiters mount and hash the base mongod configuration today.
			Configuration:            rs.Configuration,
			LivenessProbe:            rs.LivenessProbe.DeepCopy(),
			ReadinessProbe:           rs.ReadinessProbe.DeepCopy(),
			PodSecurityContext:       rs.PodSecurityContext.DeepCopy(),
			ContainerSecurityContext: rs.ContainerSecurityContext.DeepCopy(),
			Env:                      copySlice(rs.Env),
			EnvFrom:                  copySlice(rs.EnvFrom),
			Member: MemberConfig{
				ArbiterOnly: true,
				Votes:       legacyDefaultVotes,
				Priority:    0,
			},
			DataBearing:     false,
			PrimaryEligible: false,
			Source:          SourceRef{ReplsetName: rs.Name, LegacyRole: "arbiter"},
		})
	}

	if rs.NonVoting.Enabled {
		groups = append(groups, Group{
			Name:                     naming.GroupNonVoting,
			Replicas:                 rs.NonVoting.Size,
			MultiAZ:                  *rs.NonVoting.MultiAZ.DeepCopy(),
			VolumeSpec:               rs.NonVoting.VolumeSpec.DeepCopy(),
			Configuration:            rs.NonVoting.Configuration,
			LivenessProbe:            rs.NonVoting.LivenessProbe.DeepCopy(),
			ReadinessProbe:           rs.NonVoting.ReadinessProbe.DeepCopy(),
			PodSecurityContext:       rs.NonVoting.PodSecurityContext.DeepCopy(),
			ContainerSecurityContext: rs.NonVoting.ContainerSecurityContext.DeepCopy(),
			Env:                      copySlice(rs.Env),
			EnvFrom:                  copySlice(rs.EnvFrom),
			Member: MemberConfig{
				Votes:    0,
				Priority: 0,
				// The role tag legacy attaches today. SetVotes reads it, so it
				// must stay in legacy mode. Instance groups get no role tags.
				Tags: map[string]string{naming.ComponentNonVoting: "true"},
			},
			DataBearing:     true,
			PrimaryEligible: false,
			Source:          SourceRef{ReplsetName: rs.Name, LegacyRole: "nonvoting"},
		})
	}

	if rs.Hidden.Enabled {
		groups = append(groups, Group{
			Name:                     naming.GroupHidden,
			Replicas:                 rs.Hidden.Size,
			MultiAZ:                  *rs.Hidden.MultiAZ.DeepCopy(),
			VolumeSpec:               rs.Hidden.VolumeSpec.DeepCopy(),
			Configuration:            rs.Hidden.Configuration,
			LivenessProbe:            rs.Hidden.LivenessProbe.DeepCopy(),
			ReadinessProbe:           rs.Hidden.ReadinessProbe.DeepCopy(),
			PodSecurityContext:       rs.Hidden.PodSecurityContext.DeepCopy(),
			ContainerSecurityContext: rs.Hidden.ContainerSecurityContext.DeepCopy(),
			Env:                      copySlice(rs.Env),
			EnvFrom:                  copySlice(rs.EnvFrom),
			Member: MemberConfig{
				Hidden:   true,
				Votes:    legacyDefaultVotes,
				Priority: 0,
				Tags:     map[string]string{naming.ComponentHidden: "true"},
			},
			DataBearing:     true,
			PrimaryEligible: false,
			Source:          SourceRef{ReplsetName: rs.Name, LegacyRole: "hidden"},
		})
	}

	for i := range groups {
		finishIdentity(cr, rs, &groups[i])
	}

	return groups, PolicyImplicit, nil
}

func finishIdentity(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, g *Group) {
	g.ClusterRole = rs.ClusterRole
	g.Component = naming.GroupComponent(g.Name)
	if rs.ClusterRole == api.ClusterRoleConfigSvr && g.Name == naming.GroupMongod {
		g.Component = naming.ComponentConfigSrv
	}

	g.STSName = naming.GroupStatefulSetName(cr, rs, g.Name)
	g.ContainerName = naming.GroupContainerName(g.Name, g.Member.ArbiterOnly)
	g.ConfigName = naming.GroupConfigMapName(cr, rs, g.Name)

	g.Labels = naming.RSLabels(cr, rs)
	g.Labels[naming.LabelKubernetesComponent] = g.Component
}

func copySlice[T any](in []T) []T {
	if in == nil {
		return nil
	}
	out := make([]T, len(in))
	copy(out, in)
	return out
}
