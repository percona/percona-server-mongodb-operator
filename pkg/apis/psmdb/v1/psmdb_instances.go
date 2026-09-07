package v1

import (
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

const maxPodNameLen = 63

// InstancesMinCRVersion is the minimum CR version that supports the use of instances[].
const InstancesMinCRVersion = "1.24.0"

// blockedGroupNames are names that are neither reserved identities nor legal
// custom names, because the object names or component labels they produce
// collide with something the operator already owns.
var blockedGroupNames = map[string]string{
	"nv":        "it is the StatefulSet suffix of the reserved nonVoting group",
	"nonvoting": `use "nonVoting"`,
	"cfg":       "it is the config server component label",
	"mongos":    "it is the mongos component label",
	"search":    "it is the search component label",
}

func IsReservedGroupName(name string) bool {
	switch name {
	case ReservedGroupMongod, ReservedGroupNonVoting, ReservedGroupArbiter, ReservedGroupHidden:
		return true
	}
	return false
}

// InstanceSpec describes one named group of members inside a replica set.
type InstanceSpec struct {
	MultiAZ `json:",inline"`

	// Name is the group's stable identity. It is part of every Kubernetes object
	// name derived for the group, so it cannot be changed in place: renaming a
	// group removes it and creates a new one, which requires an initial sync
	// onto fresh storage.
	//
	// Must be unique within the replica set. Either one of the reserved names
	// (mongod, nonVoting, arbiter, hidden), which reproduce the object names of
	// the equivalent legacy role, or a lowercase DNS-1123 label.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=54
	Name string `json:"name"`

	// Replicas is the number of members in this group. An explicit 0 keeps the
	// group declared but scaled down; it is never defaulted to a running size.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	Replicas int32 `json:"replicas"`

	// RSConfig carries the member document fields this group controls.
	// Omitting it entirely selects the group's default member behaviour, which
	// for a reserved name is the behaviour of the equivalent legacy role.
	RSConfig *MemberConfigSpec `json:"rsConfig,omitempty"`

	// VolumeSpec replaces the replica set's storage for this group. Required
	// for every data-bearing group. Must be absent for an arbiter-only group.
	VolumeSpec *VolumeSpec `json:"volumeSpec,omitempty"`

	// Configuration replaces the replica set's mongod configuration for this
	// group. An explicit empty string means "no custom configuration", which is
	// distinct from omitting the field.
	Configuration *MongoConfiguration `json:"configuration,omitempty"`

	ReadinessProbe           *corev1.Probe              `json:"readinessProbe,omitempty"`
	LivenessProbe            *LivenessProbeExtended     `json:"livenessProbe,omitempty"`
	PodSecurityContext       *corev1.PodSecurityContext `json:"podSecurityContext,omitempty"`
	ContainerSecurityContext *corev1.SecurityContext    `json:"containerSecurityContext,omitempty"`
	Env                      []corev1.EnvVar            `json:"env,omitempty"`
	EnvFrom                  []corev1.EnvFromSource     `json:"envFrom,omitempty"`
}

// MemberConfigSpec exposes the subset of MongoDB replica set member document fields a group controls.
type MemberConfigSpec struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1000
	Priority *int32 `json:"priority,omitempty"`

	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	Votes *int32 `json:"votes,omitempty"`

	// require a direct connection: they are not reachable through normal
	// tag-based secondary routing or through mongos.
	Hidden *bool `json:"hidden,omitempty"`

	BuildIndexes *bool `json:"buildIndexes,omitempty"`

	ArbiterOnly *bool `json:"arbiterOnly,omitempty"`
	// +kubebuilder:validation:Minimum=0
	SecondaryDelaySecs *int64 `json:"secondaryDelaySecs,omitempty"`

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

func (i InstanceSpec) GetPriority(def int32) int32 {
	if i.RSConfig == nil || i.RSConfig.Priority == nil {
		return def
	}
	return *i.RSConfig.Priority
}

func (i InstanceSpec) GetVotes(def int32) int32 {
	if i.RSConfig == nil || i.RSConfig.Votes == nil {
		return def
	}
	return *i.RSConfig.Votes
}

func (i InstanceSpec) IsHidden() bool {
	return i.RSConfig != nil && i.RSConfig.Hidden != nil && *i.RSConfig.Hidden
}

func (i InstanceSpec) IsArbiterOnly() bool {
	return i.RSConfig != nil && i.RSConfig.ArbiterOnly != nil && *i.RSConfig.ArbiterOnly
}

// BuildsIndexes defaults to true, matching the MongoDB default and the value
// the operator has always written.
func (i InstanceSpec) BuildsIndexes() bool {
	if i.RSConfig == nil || i.RSConfig.BuildIndexes == nil {
		return true
	}
	return *i.RSConfig.BuildIndexes
}

func (i InstanceSpec) GetDelaySecs() *int64 {
	if i.RSConfig == nil {
		return nil
	}
	return i.RSConfig.SecondaryDelaySecs
}

func (i InstanceSpec) GetTags() map[string]string {
	if i.RSConfig == nil {
		return nil
	}
	return i.RSConfig.Tags
}

func (rs *ReplsetSpec) validateForInstances() error {
	if rs == nil || !rs.InstanceMode() {
		return nil
	}

	if rs.Size != 0 {
		return errors.Errorf("spec.replsets[%s].size must be 0 or absent when instances is set", rs.Name)
	}

	if rs.Arbiter.Enabled {
		return errors.Errorf("spec.replsets[%s].arbiter.enabled must be false when instances is set", rs.Name)
	}

	if rs.NonVoting.Enabled {
		return errors.Errorf("spec.replsets[%s].nonvoting.enabled must be false when instances is set", rs.Name)
	}

	if rs.Hidden.Enabled {
		return errors.Errorf("spec.replsets[%s].hidden.enabled must be false when instances is set", rs.Name)
	}
	return nil
}
