package v1

import (
	"fmt"
	"slices"

	"github.com/percona/percona-server-mongodb-operator/pkg/version"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
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

	// Name of this member group. Must be unique within the replica set.
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

	if rs.ClusterRole == ClusterRoleConfigSvr {
		return errors.Errorf("spec.replsets[%s].instances is not supported for clusterRole %s: "+
			"declare the config server replica set with size", rs.Name, ClusterRoleConfigSvr)
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

	for _, ins := range rs.Instances {
		if err := ins.validate(rs.Name); err != nil {
			return err
		}
	}

	// TODO: we need to validate name collisions between every StatefulSet name the CR would produce.
	// For example: group "hot" in "rs0" and a base replica set named "rs0-hot" both will produce a StatefulSet named "rs0-hot".

	return nil
}

func (is *InstanceSpec) validate(rsName string) error {
	if is == nil {
		return nil
	}

	if reason, blocked := blockedGroupNames[is.Name]; blocked {
		return errors.Errorf("spec.replsets[%s].instances: instance name %q is reserved: %s",
			rsName, is.Name, reason)
	}

	if IsReservedGroupName(is.Name) {
		return nil
	}

	if errs := validation.IsDNS1123Label(is.Name); len(errs) > 0 {
		return errors.Errorf("spec.replsets[%s].instances[%s].name must be a valid DNS-1123 label: %v", rsName, is.Name, errs)
	}
	return nil
}

func (r *ReplsetSpec) groupNames() []string {
	if r.InstanceMode() {
		names := make([]string, 0, len(r.Instances))
		for i := range r.Instances {
			names = append(names, r.Instances[i].Name)
		}
		return names
	}

	names := []string{ReservedGroupMongod}
	if r.Arbiter.Enabled {
		names = append(names, ReservedGroupArbiter)
	}
	if r.NonVoting.Enabled {
		names = append(names, ReservedGroupNonVoting)
	}
	if r.Hidden.Enabled {
		names = append(names, ReservedGroupHidden)
	}
	return names
}

func (r *ReplsetSpec) groupReplicas(name string) int32 {
	if r.InstanceMode() {
		if i := r.Instance(name); i != nil {
			return i.Replicas
		}
		return 0
	}
	switch name {
	case ReservedGroupArbiter:
		return r.Arbiter.GetSize()
	case ReservedGroupNonVoting:
		return r.NonVoting.GetSize()
	case ReservedGroupHidden:
		return r.Hidden.GetSize()
	default:
		return r.Size
	}
}

func (i *InstanceSpec) SetDefaults(platform version.Platform, cr *PerconaServerMongoDB, rs *ReplsetSpec) error {
	path := fmt.Sprintf("spec.replsets[%s].instances[%s]", rs.Name, i.Name)

	if i.IsArbiterOnly() {
		if i.VolumeSpec != nil {
			return errors.Errorf("%s: arbiter-only instance must not declare volumeSpec: "+
				"arbiters use an emptyDir for the mongod data path", path)
		}
	} else {
		if i.VolumeSpec == nil {
			return errors.Errorf("%s.volumeSpec is required for a data-bearing instance", path)
		}
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

	return i.validateMemberConfig(rs)
}

// TODO
func (i *InstanceSpec) validateMemberConfig(rs *ReplsetSpec) error { return nil }
