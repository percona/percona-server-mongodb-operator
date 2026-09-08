package v1

import (
	"fmt"
	"slices"

	"github.com/percona/percona-server-mongodb-operator/pkg/version"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
)

// These instance names are reserved for producing the equivalent legacy Kubernetes objects (i.e, without the use of instances[]).
const (
	ReservedGroupMongod    = "mongod"
	ReservedGroupNonVoting = "nonVoting"
	ReservedGroupArbiter   = "arbiter"
	ReservedGroupHidden    = "hidden"
)

const (
	maxReplsetMembers        = 50
	maxVotingMembers         = 7
	minSafeDataBearingVoters = 3

	defaultInstancePriority = 2
	defaultVotes            = 1
)

// InstancesMinCRVersion is the minimum CR version that supports the use of instances[].
const InstancesMinCRVersion = "1.24.0"

func IsReservedGroupName(name string) bool {
	switch name {
	case ReservedGroupMongod, ReservedGroupNonVoting, ReservedGroupArbiter, ReservedGroupHidden:
		return true
	}
	return false
}

// InstanceSpec describes one named group of members inside a replica set.
// +kubebuilder:validation:XValidation:rule="self.?rsConfig.?arbiterOnly.orValue(false) || has(self.volumeSpec)",message="volumeSpec is required for a data-bearing instance"
// +kubebuilder:validation:XValidation:rule="!self.?rsConfig.?arbiterOnly.orValue(false) || !has(self.volumeSpec)",message="arbiterOnly instance must not declare volumeSpec: arbiters use an emptyDir for the mongod data path"
// +kubebuilder:validation:XValidation:rule="!(self.name in ['nv','nonvoting','cfg','mongos','search'])",message="instance name is reserved by the operator: nv, nonvoting, cfg, mongos and search collide with generated object names or component labels (did you mean nonVoting?)"
type InstanceSpec struct {
	MultiAZ `json:",inline"`

	// Name of this member group. Must be unique within the replica set.

	// +kubebuilder:validation:XValidation:rule="format.dns1123Label().validate(self).hasValue()",message="instance name should be a valid dns1123 label"
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=54
	Name string `json:"name"`

	// Replicas is the number of members in this group.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	Replicas int32 `json:"replicas"`

	// RSConfig carries the member document fields this group controls.
	// Omitting it entirely selects the group's default member behaviour, which
	// for a reserved name is the behaviour of the equivalent legacy role.
	RSConfig *MemberConfigSpec `json:"rsConfig,omitempty"`

	// VolumeSpec specifies the replica set's storage for this group.
	VolumeSpec *VolumeSpec `json:"volumeSpec,omitempty"`

	// Specifiying the following fields will override the corresponding replicaset-level settings.

	ReadinessProbe           *corev1.Probe              `json:"readinessProbe,omitempty"`
	LivenessProbe            *LivenessProbeExtended     `json:"livenessProbe,omitempty"`
	PodSecurityContext       *corev1.PodSecurityContext `json:"podSecurityContext,omitempty"`
	ContainerSecurityContext *corev1.SecurityContext    `json:"containerSecurityContext,omitempty"`
	Env                      []corev1.EnvVar            `json:"env,omitempty"`
	EnvFrom                  []corev1.EnvFromSource     `json:"envFrom,omitempty"`
}

// MemberConfigSpec exposes the subset of MongoDB replica set member document fields a group controls.
// +kubebuilder:validation:XValidation:rule="!self.?arbiterOnly.orValue(false) || !self.?hidden.orValue(false)",message="arbiterOnly instance must not be hidden"
// +kubebuilder:validation:XValidation:rule="!self.?arbiterOnly.orValue(false) || self.?votes.orValue(1) == 1",message="arbiterOnly instance must have votes=1"
// +kubebuilder:validation:XValidation:rule="!self.?arbiterOnly.orValue(false) || self.?priority.orValue(0) == 0",message="arbiterOnly instance must have priority=0"
// +kubebuilder:validation:XValidation:rule="!self.?arbiterOnly.orValue(false) || !has(self.tags)",message="arbiterOnly instance must not have tags"
// +kubebuilder:validation:XValidation:rule="self.?votes.orValue(1) != 0 || self.?priority.orValue(0) == 0",message="votes=0 requires priority=0"
// +kubebuilder:validation:XValidation:rule="!self.?hidden.orValue(false) || self.?priority.orValue(0) == 0",message="hidden=true requires priority=0"
type MemberConfigSpec struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1000
	Priority *int32 `json:"priority,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	Votes *int32 `json:"votes,omitempty"`

	// Hidden keeps the member out of the hello command output. Hidden members
	// require a direct connection: they are not reachable through normal
	// tag-based secondary routing or through mongos.
	Hidden *bool `json:"hidden,omitempty"`

	ArbiterOnly *bool `json:"arbiterOnly,omitempty"`

	// +kubebuilder:validation:MaxProperties=32
	Tags map[string]string `json:"tags,omitempty"`
}

// InstanceMode returns true if the replia set declares topology through instances[].
func (r *ReplsetSpec) InstanceMode() bool {
	return len(r.Instances) > 0
}

// Instance returns a pointer into r.Instances, or nil.
// The pointer aliases the slice element so defaulting can mutate it in place.
func (r *ReplsetSpec) Instance(name string) *InstanceSpec {
	if name == "" {
		return nil
	}
	for i := range r.Instances {
		if r.Instances[i].Name == name {
			return &r.Instances[i]
		}
	}
	return nil
}

// HasMemberConfig reports whether the group supplied an rsConfig object at all,
// including an empty one. This is what selects the replica-set compatibility
// policy: a reserved group with no rsConfig keeps legacy vote behaviour.
func (i InstanceSpec) HasMemberConfig() bool {
	return i.RSConfig != nil
}

func (i InstanceSpec) GetPriority() int32 {
	if i.RSConfig == nil || i.RSConfig.Priority == nil {
		return defaultInstancePriority
	}
	return *i.RSConfig.Priority
}

func (i InstanceSpec) GetVotes() int32 {
	if i.RSConfig == nil || i.RSConfig.Votes == nil {
		return defaultVotes
	}
	return *i.RSConfig.Votes
}

func (i InstanceSpec) IsHidden() bool {
	return i.RSConfig != nil && i.RSConfig.Hidden != nil && *i.RSConfig.Hidden
}

func (i InstanceSpec) IsArbiterOnly() bool {
	return i.RSConfig != nil && i.RSConfig.ArbiterOnly != nil && *i.RSConfig.ArbiterOnly
}

func (i InstanceSpec) GetTags() map[string]string {
	if i.RSConfig == nil {
		return nil
	}
	return i.RSConfig.Tags
}

func (i *InstanceSpec) SetDefaults(platform version.Platform, cr *PerconaServerMongoDB, rs *ReplsetSpec) error {
	path := fmt.Sprintf("spec.replsets[%s].instances[%s]", rs.Name, i.Name)

	// Presence and absence of volumeSpec are CEL rules; only the option
	// normalization is left.
	if i.VolumeSpec != nil {
		if err := i.VolumeSpec.reconcileOpts(); err != nil {
			return errors.Wrapf(err, "%s.volumeSpec", path)
		}
	}

	if i.LivenessProbe == nil {
		i.LivenessProbe = rs.LivenessProbe.DeepCopy()
	} else {
		i.LivenessProbe = defaultLivenessProbe(cr, i.LivenessProbe)
	}

	if i.ReadinessProbe == nil {
		i.ReadinessProbe = rs.ReadinessProbe.DeepCopy()
	} else {
		i.ReadinessProbe = defaultReadinessProbe(cr, i.ReadinessProbe, rs.Name, int(rs.GetPort()))
	}

	if len(i.Env) == 0 {
		i.Env = slices.Clone(rs.Env)
	}

	if len(i.EnvFrom) == 0 {
		i.EnvFrom = slices.Clone(rs.EnvFrom)
	}

	if i.PodSecurityContext == nil {
		i.PodSecurityContext = rs.PodSecurityContext.DeepCopy()
	}
	if i.ContainerSecurityContext == nil {
		i.ContainerSecurityContext = rs.ContainerSecurityContext.DeepCopy()
	}

	if i.ServiceAccountName == "" {
		i.ServiceAccountName = rs.ServiceAccountName
	}

	//nolint:staticcheck
	if err := i.MultiAZ.reconcileOpts(cr); err != nil {
		return errors.Wrapf(err, "%s: reconcile multiAZ options", path)
	}

	return nil
}

// ResolvedPriority returns the effective priority of the instance, taking into account arbiter, hidden, and voting status.
func (i InstanceSpec) ResolvedPriority() int32 {
	if i.IsArbiterOnly() || i.IsHidden() || i.GetVotes() == 0 {
		return 0
	}
	return i.GetPriority()
}

// IsPrimaryEligible returns true if the instance is eligible to become the primary in the replica set.
func (i InstanceSpec) IsPrimaryEligible() bool {
	return i.Replicas > 0 && i.GetVotes() > 0 && i.ResolvedPriority() > 0
}

// IsDataBearing returns true if the instance holds data, i.e., it is not an arbiter.
func (i InstanceSpec) IsDataBearing() bool { return !i.IsArbiterOnly() }

// LegacyVotePolicy reports whether this replica set keeps the historical vote
// algorithm (mongo.ConfigMembers.SetVotes). True for a legacy topology, and for
// an instances[] topology whose groups are all reserved names with no rsConfig
// at all: such input carries no explicit member intent, so the reserved names
// still mean what they always meant. Any custom group, or any rsConfig on any
// group, makes the whole replica set explicit.
//
// This is the single source of truth for the decision. membergroup.Resolve maps
// it onto membergroup.PolicyLegacy / PolicyExplicit, and instanceCounts and
// checkSafeInstanceDefaults consult it so that validation never enforces an
// invariant the vote engine is not going to be asked to uphold.
func (r *ReplsetSpec) UseLegacyVotePolicy() bool {
	if !r.InstanceMode() {
		return true
	}
	for i := range r.Instances {
		if !IsReservedGroupName(r.Instances[i].Name) || r.Instances[i].HasMemberConfig() {
			return false
		}
	}
	return true
}

// resolvedMember returns the (votes, priority, dataBearing) for the instance.
func (i InstanceSpec) resolvedMember(legacy bool) (votes, priority int32, dataBearing bool) {
	if legacy {
		switch i.Name {
		case ReservedGroupArbiter:
			return 1, 0, false
		case ReservedGroupNonVoting:
			return 0, 0, true
		case ReservedGroupHidden:
			return 1, 0, true
		default: // ReservedGroupMongod
			return 1, defaultInstancePriority, true
		}
	}
	return i.GetVotes(), i.ResolvedPriority(), i.IsDataBearing()
}

func (r *ReplsetSpec) instanceCounts() (members, voters, dataBearingVoters, primaryEligible int32) {
	legacyPolicy := r.UseLegacyVotePolicy()

	for j := range r.Instances {
		i := &r.Instances[j]
		votes, priority, dataBearing := i.resolvedMember(legacyPolicy)

		members += i.Replicas
		if votes > 0 {
			voters += i.Replicas
			if dataBearing {
				dataBearingVoters += i.Replicas
			}
		}
		if i.Replicas > 0 && votes > 0 && priority > 0 {
			primaryEligible += i.Replicas
		}
	}

	for _, ext := range r.ExternalNodes {
		if ext == nil {
			continue
		}
		members++
		if ext.Votes > 0 {
			voters++
			if !ext.ArbiterOnly {
				dataBearingVoters++
			}
		}
	}

	return members, voters, dataBearingVoters, primaryEligible
}

func DerivedStatefulSetName(clusterName, rsName, groupName string) string {
	base := clusterName + "-" + rsName
	switch groupName {
	case ReservedGroupMongod:
		return base
	case ReservedGroupNonVoting:
		return base + "-nv"
	default:
		return base + "-" + groupName
	}
}
