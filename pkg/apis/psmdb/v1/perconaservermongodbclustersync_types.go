package v1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PerconaServerMongoDBClusterSyncSpec uses CR existence as the lifecycle
// signal — there is no `enabled` field; creating starts replication and
// deleting tears down owned resources.
type PerconaServerMongoDBClusterSyncSpec struct {
	ClusterName string `json:"clusterName"`
	Image       string `json:"image"`

	ImagePullPolicy  corev1.PullPolicy             `json:"imagePullPolicy,omitempty"`
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	Resources                corev1.ResourceRequirements `json:"resources,omitempty"`
	NodeSelector             map[string]string           `json:"nodeSelector,omitempty"`
	Tolerations              []corev1.Toleration         `json:"tolerations,omitempty"`
	Affinity                 *PodAffinity                `json:"affinity,omitempty"`
	Annotations              map[string]string           `json:"annotations,omitempty"`
	Labels                   map[string]string           `json:"labels,omitempty"`
	RuntimeClassName         *string                     `json:"runtimeClassName,omitempty"`
	ContainerSecurityContext *corev1.SecurityContext     `json:"containerSecurityContext,omitempty"`
	PodSecurityContext       *corev1.PodSecurityContext  `json:"podSecurityContext,omitempty"`

	LivenessProbe  *corev1.Probe `json:"livenessProbe,omitempty"`
	ReadinessProbe *corev1.Probe `json:"readinessProbe,omitempty"`

	Source ClusterSyncSource `json:"source"`

	// ExcludeNamespaces lists MongoDB namespaces
	ExcludeNamespaces []string `json:"excludeNamespaces,omitempty"`

	// Mode is the user intent for the PCSM lifecycle and the sole driver
	// of the pcsm start/pause/resume/finalize CLI calls the operator
	// issues against the PCSM container.
	// +kubebuilder:default=running
	Mode ClusterSyncMode `json:"mode,omitempty"`

	// PCSMConfig holds the small set of PCSM tuning knobs we expose as typed
	// fields. Anything not listed here goes through Env.
	PCSMConfig ClusterSyncPCSMConfig `json:"pcsmConfig,omitempty"`

	Env []corev1.EnvVar `json:"env,omitempty"`
}

// ClusterSyncPCSMConfig holds PCSM knobs that have no `PCSM_*` env-var
// equivalent and must be passed as CLI args.
type ClusterSyncPCSMConfig struct {
	// LogLevel maps to `--log-level`.
	// +kubebuilder:validation:Enum={debug,info,warn,error}
	LogLevel string `json:"logLevel,omitempty"`

	// LogJSON maps to `--log-json`. Pointer so the user can explicitly
	// disable structured logging even if a future default flips.
	LogJSON *bool `json:"logJSON,omitempty"`
}

// ClusterSyncMode is the user-controlled lifecycle intent for PCSM.
// +kubebuilder:validation:Enum={paused,running,finalized}
type ClusterSyncMode string

const (
	ClusterSyncModePaused    ClusterSyncMode = "paused"
	ClusterSyncModeRunning   ClusterSyncMode = "running"
	ClusterSyncModeFinalized ClusterSyncMode = "finalized"
)

type ClusterSyncSource struct {
	// URI is the source connection string WITHOUT credentials; credentials
	// from CredentialsSecret are injected at runtime, percent-encoded.
	URI string `json:"uri"`

	// CredentialsSecret is a same-namespace Secret with `username` and
	// `password` keys.
	CredentialsSecret string `json:"credentialsSecret"`
}

// ClusterSyncState mirrors the state strings PCSM returns from
// `pcsm status`. Source of truth: PCSM docs
// (https://docs.percona.com/percona-clustersync-for-mongodb/intro.html).
type ClusterSyncState string

const (
	ClusterSyncStateIdle       ClusterSyncState = "idle"
	ClusterSyncStateRunning    ClusterSyncState = "running"
	ClusterSyncStatePaused     ClusterSyncState = "paused"
	ClusterSyncStateFinalizing ClusterSyncState = "finalizing"
	ClusterSyncStateFinalized  ClusterSyncState = "finalized"
	ClusterSyncStateFailed     ClusterSyncState = "failed"
)

const (
	ConditionClusterSyncRunning   = "Running"
	ConditionClusterSyncFinalized = "Finalized"
)

type PerconaServerMongoDBClusterSyncStatus struct {
	// Mode mirrors spec.mode after the corresponding HTTP call has
	// succeeded; the controller compares the two to decide whether a
	// transition is required. Empty until the first mode has been
	// applied.
	Mode ClusterSyncMode `json:"mode,omitempty"`

	// State is the PCSM-reported runtime state from GET /status, distinct
	// from spec.mode/status.mode: those are user intent, this is what
	// PCSM is actually doing.
	State ClusterSyncState `json:"state,omitempty"`

	LagTimeSeconds int64  `json:"lagTimeSeconds,omitempty"`
	Error          string `json:"error,omitempty"`

	// StartedAt is set once the first time PCSM reports state=running;
	// not updated on restart.
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerconaServerMongoDBClusterSync is the Schema for the
// perconaservermongodbclustersyncs API.
// +k8s:openapi-gen=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName="psmdb-clustersync"
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=".spec.clusterName",description="Target cluster name"
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=".spec.mode",description="User-requested lifecycle mode"
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=".status.state",description="Replication state"
// +kubebuilder:printcolumn:name="Lag(s)",type=integer,JSONPath=".status.lagTimeSeconds",description="Replication lag in seconds"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp",description="Created time"
type PerconaServerMongoDBClusterSync struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PerconaServerMongoDBClusterSyncSpec   `json:"spec,omitempty"`
	Status PerconaServerMongoDBClusterSyncStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerconaServerMongoDBClusterSyncList contains a list of PerconaServerMongoDBClusterSync.
type PerconaServerMongoDBClusterSyncList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PerconaServerMongoDBClusterSync `json:"items"`
}
