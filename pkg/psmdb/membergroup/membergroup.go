package membergroup

import (
	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

// Policy is the replicaset-level vote-management policy.
type Policy string

const (
	// PolicyImplicit is used when a replica-set is configured without instances[].
	PolicyImplicit Policy = "implicit"
	// PolicyExplicit is used when a replica-set is configured with instances[].
	PolicyExplicit Policy = "explicit"
)

// MemberConfig is the rs.conf() member document a group contributes, with every value resolved.
type MemberConfig struct {
	Priority    int
	Votes       int
	Hidden      bool
	ArbiterOnly bool
	Tags        map[string]string
}

type SourceRef struct {
	ReplsetName string
	// InstanceName is the instances[] entry that owns the group; empty in legacy mode.
	InstanceName string
	// LegacyRole names the legacy block that owns the group in legacy mode:
	// "" for the base replica set, or "arbiter" / "nonvoting" / "hidden".
	LegacyRole string
}

// Group is a resolved, self-contained description of one group of members.
type Group struct {
	Name          string
	Component     string
	ClusterRole   api.ClusterRole
	STSName       string
	ContainerName string
	ConfigName    string

	// Labels are the workload's selector labels. For an existing StatefulSet
	// these go into an immutable selector, so they must never change.
	Labels map[string]string

	// Replicas is the user-requested member count. It is not the temporary
	// count a reconciliation may use during downscale, shutdown or restore —
	// callers track that separately.
	Replicas int32

	// Pod configuration
	MultiAZ                  api.MultiAZ
	VolumeSpec               *api.VolumeSpec
	Configuration            api.MongoConfiguration
	LivenessProbe            *api.LivenessProbeExtended
	ReadinessProbe           *corev1.Probe
	PodSecurityContext       *corev1.PodSecurityContext
	ContainerSecurityContext *corev1.SecurityContext
	Env                      []corev1.EnvVar
	EnvFrom                  []corev1.EnvFromSource

	// MongoDB configuration
	Member MemberConfig

	// Capabilities

	// DataBearing is false only for arbiters: they use an emptyDir for the
	// mongod data path and get no operator-managed PBM, PMM or log-collector
	// containers.
	DataBearing bool
	// PrimaryEligible is a configuration filter, not the final word. A per-pod
	// priority override or a primaryPreferTagSelector match can change a
	// particular member's eligibility, and an observed primary can stay primary
	// while its desired settings change. Use the final per-pod member settings
	// for eligibility and live replica set status for transition decisions.
	PrimaryEligible bool

	Source SourceRef
}

// PDB returns the pod disruption budget spec for the group, or nil.
func (g *Group) PDB() *api.PodDisruptionBudgetSpec {
	return g.MultiAZ.PodDisruptionBudget
}

// DesiredPodNames returns the pod names for ordinals [0, Replicas).
func (g *Group) DesiredPodNames(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) []string {
	names := make([]string, 0, g.Replicas)
	for i := 0; i < int(g.Replicas); i++ {
		names = append(names, naming.GroupPodName(cr, rs, g.Name, i))
	}
	return names
}

// OwnsPod reports whether the pod belongs to this group.
func (g *Group) OwnsPod(pod *corev1.Pod) bool {
	return pod.Labels[naming.LabelKubernetesComponent] == g.Component
}
