package v1

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	"github.com/go-logr/logr"
	v "github.com/hashicorp/go-version"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	k8sversion "k8s.io/apimachinery/pkg/version"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/defs"

	"github.com/percona/percona-server-mongodb-operator/pkg/mcs"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/percona/percona-server-mongodb-operator/pkg/util/numstr"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerconaServerMongoDB is the Schema for the perconaservermongodbs API
// +k8s:openapi-gen=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName="psmdb"
// +kubebuilder:printcolumn:name="ENDPOINT",type="string",JSONPath=".status.host"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type PerconaServerMongoDB struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PerconaServerMongoDBSpec   `json:"spec,omitempty"`
	Status PerconaServerMongoDBStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerconaServerMongoDBList contains a list of PerconaServerMongoDB
type PerconaServerMongoDBList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PerconaServerMongoDB `json:"items"`
}

type ClusterRole string

const (
	ClusterRoleShardSvr  ClusterRole = "shardsvr"
	ClusterRoleConfigSvr ClusterRole = "configsvr"
)

// PerconaServerMongoDBSpec defines the desired state of PerconaServerMongoDB
type PerconaServerMongoDBSpec struct {
	Pause                        bool                                 `json:"pause,omitempty"`
	Unmanaged                    bool                                 `json:"unmanaged,omitempty"`
	CRVersion                    string                               `json:"crVersion,omitempty"`
	Platform                     *version.Platform                    `json:"platform,omitempty"`
	Image                        string                               `json:"image"`
	ImagePullSecrets             []corev1.LocalObjectReference        `json:"imagePullSecrets,omitempty"`
	UnsafeConf                   bool                                 `json:"allowUnsafeConfigurations,omitempty"`
	Unsafe                       UnsafeFlags                          `json:"unsafeFlags,omitempty"`
	IgnoreLabels                 []string                             `json:"ignoreLabels,omitempty"`
	IgnoreAnnotations            []string                             `json:"ignoreAnnotations,omitempty"`
	Replsets                     []*ReplsetSpec                       `json:"replsets,omitempty"`
	Secrets                      *SecretsSpec                         `json:"secrets,omitempty"`
	Backup                       BackupSpec                           `json:"backup,omitempty"`
	ImagePullPolicy              corev1.PullPolicy                    `json:"imagePullPolicy,omitempty"`
	PMM                          PMMSpec                              `json:"pmm,omitempty"`
	UpdateStrategy               appsv1.StatefulSetUpdateStrategyType `json:"updateStrategy,omitempty"`
	UpgradeOptions               UpgradeOptions                       `json:"upgradeOptions,omitempty"`
	SchedulerName                string                               `json:"schedulerName,omitempty"`
	ClusterServiceDNSSuffix      string                               `json:"clusterServiceDNSSuffix,omitempty"`
	ClusterServiceDNSMode        DNSMode                              `json:"clusterServiceDNSMode,omitempty"`
	Sharding                     Sharding                             `json:"sharding,omitempty"`
	InitImage                    string                               `json:"initImage,omitempty"`
	InitContainerSecurityContext *corev1.SecurityContext              `json:"initContainerSecurityContext,omitempty"`
	MultiCluster                 MultiCluster                         `json:"multiCluster,omitempty"`
	TLS                          *TLSSpec                             `json:"tls,omitempty"`
	Users                        []User                               `json:"users,omitempty"`
	Roles                        []Role                               `json:"roles,omitempty"`
	VolumeExpansionEnabled       bool                                 `json:"enableVolumeExpansion,omitempty"`
	LogCollector                 *LogCollectorSpec                    `json:"logcollector,omitempty"`
}

type UserRole struct {
	Name string `json:"name"`
	DB   string `json:"db"`
}

type SecretKeySelector struct {
	Name string `json:"name"`
	Key  string `json:"key,omitempty"`
}

type User struct {
	Name              string             `json:"name"`
	DB                string             `json:"db,omitempty"`
	PasswordSecretRef *SecretKeySelector `json:"passwordSecretRef,omitempty"`
	Roles             []UserRole         `json:"roles"`
}

func (u *User) UserID() string {
	return u.DB + "." + u.Name
}

func (u *User) IsExternalDB() bool {
	return u.DB == "$external"
}

type RoleAuthenticationRestriction struct {
	ClientSource  []string `json:"clientSource,omitempty"`
	ServerAddress []string `json:"serverAddress,omitempty"`
}

type RoleResource struct {
	Collection string `json:"collection,omitempty"`
	DB         string `json:"db,omitempty"`
	Cluster    *bool  `json:"cluster,omitempty"`
}

type RolePrivilege struct {
	Actions  []string     `json:"actions"`
	Resource RoleResource `json:"resource,omitempty"`
}

type InheritenceRole struct {
	Role string `json:"role"`
	DB   string `json:"db"`
}

type Role struct {
	Role                       string                          `json:"role"`
	DB                         string                          `json:"db"`
	Privileges                 []RolePrivilege                 `json:"privileges"`
	AuthenticationRestrictions []RoleAuthenticationRestriction `json:"authenticationRestrictions,omitempty"`
	Roles                      []InheritenceRole               `json:"roles,omitempty"`
}

type UnsafeFlags struct {
	TLS                    bool `json:"tls,omitempty"`
	ReplsetSize            bool `json:"replsetSize,omitempty"`
	MongosSize             bool `json:"mongosSize,omitempty"`
	TerminationGracePeriod bool `json:"terminationGracePeriod,omitempty"`
	BackupIfUnhealthy      bool `json:"backupIfUnhealthy,omitempty"`
}

type TLSMode string

const (
	TLSModeDisabled TLSMode = "disabled"
	TLSModeAllow    TLSMode = "allowTLS"
	TLSModePrefer   TLSMode = "preferTLS"
	TLSModeRequire  TLSMode = "requireTLS"
)

type TLSSpec struct {
	Mode                     TLSMode                 `json:"mode,omitempty"`
	AllowInvalidCertificates *bool                   `json:"allowInvalidCertificates,omitempty"`
	CertValidityDuration     metav1.Duration         `json:"certValidityDuration,omitempty"`
	IssuerConf               *cmmeta.ObjectReference `json:"issuerConf,omitempty"`
}

func (spec *PerconaServerMongoDBSpec) Replset(name string) *ReplsetSpec {
	switch name {
	case "":
		return nil
	case ConfigReplSetName:
		return spec.Sharding.ConfigsvrReplSet
	}
	for _, rs := range spec.Replsets {
		if rs != nil && rs.Name == name {
			return rs
		}
	}
	return nil
}

const (
	SmartUpdateStatefulSetStrategyType appsv1.StatefulSetUpdateStrategyType = "SmartUpdate"
)

// DNSMode string describes the mode used to generate fqdn/ip for communication between nodes
// +enum
type DNSMode string

const (
	// DNSModeServiceMesh means a FQDN (<pod>.<ns>.svc.cluster.local) will be generated,
	// assumming the FQDN is resolvable and available in all clusters
	DNSModeServiceMesh DNSMode = "ServiceMesh"

	// DNSModeInternal means the local FQDN (<pod>.<svc>.<ns>.svc.cluster.local) will be used
	DNSModeInternal DNSMode = "Internal"

	// DNSModeExternal means external IPs will be used in case of the services are exposed
	DNSModeExternal DNSMode = "External"
)

type Sharding struct {
	Enabled          bool         `json:"enabled"`
	ConfigsvrReplSet *ReplsetSpec `json:"configsvrReplSet,omitempty"`
	Mongos           *MongosSpec  `json:"mongos,omitempty"`
	Balancer         BalancerSpec `json:"balancer,omitempty"`
}

type BalancerSpec struct {
	Enabled *bool `json:"enabled,omitempty"`
}

func (b *BalancerSpec) IsEnabled() bool {
	return b.Enabled == nil || *b.Enabled
}

type UpgradeOptions struct {
	VersionServiceEndpoint string          `json:"versionServiceEndpoint,omitempty"`
	Apply                  UpgradeStrategy `json:"apply,omitempty"`
	Schedule               string          `json:"schedule,omitempty"`
	SetFCV                 bool            `json:"setFCV,omitempty"`
}

type ReplsetMemberStatus struct {
	Name     string            `json:"name,omitempty"`
	State    mongo.MemberState `json:"state,omitempty"`
	StateStr string            `json:"stateStr,omitempty"`
}

type MongosStatus struct {
	Size    int      `json:"size"`
	Ready   int      `json:"ready"`
	Status  AppState `json:"status,omitempty"`
	Message string   `json:"message,omitempty"`
}

type ReplsetStatus struct {
	Members     map[string]ReplsetMemberStatus `json:"members,omitempty"`
	ClusterRole ClusterRole                    `json:"clusterRole,omitempty"`

	Initialized  bool     `json:"initialized,omitempty"`
	AddedAsShard *bool    `json:"added_as_shard,omitempty"`
	Size         int32    `json:"size"`
	Ready        int32    `json:"ready"`
	Status       AppState `json:"status,omitempty"`
	Message      string   `json:"message,omitempty"`
}

type MultiCluster struct {
	Enabled   bool   `json:"enabled"`
	DNSSuffix string `json:"DNSSuffix,omitempty"`
}

type AppState string

const (
	AppStateNone     AppState = ""
	AppStateInit     AppState = "initializing"
	AppStateStopping AppState = "stopping"
	AppStatePaused   AppState = "paused"
	AppStateReady    AppState = "ready"
	AppStateError    AppState = "error"

	AppStateSharding AppState = "sharding"
)

type UpgradeStrategy string

func (us UpgradeStrategy) Lower() UpgradeStrategy {
	return UpgradeStrategy(strings.ToLower(string(us)))
}

func OneOfUpgradeStrategy(a string) bool {
	us := UpgradeStrategy(strings.ToLower(a))

	return us == UpgradeStrategyLatest ||
		us == UpgradeStrategyRecommended ||
		us == UpgradeStrategyDisabled ||
		us == UpgradeStrategyNever
}

const (
	UpgradeStrategyDisabled    UpgradeStrategy = "disabled"
	UpgradeStrategyNever       UpgradeStrategy = "never"
	UpgradeStrategyRecommended UpgradeStrategy = "recommended"
	UpgradeStrategyLatest      UpgradeStrategy = "latest"
)

const DefaultVersionServiceEndpoint = "https://check.percona.com"

func GetDefaultVersionServiceEndpoint() string {
	if endpoint := os.Getenv("PERCONA_VS_FALLBACK_URI"); len(endpoint) > 0 {
		return endpoint
	}

	return DefaultVersionServiceEndpoint
}

// PerconaServerMongoDBStatus defines the observed state of PerconaServerMongoDB
type PerconaServerMongoDBStatus struct {
	State              AppState                 `json:"state,omitempty"`
	MongoVersion       string                   `json:"mongoVersion,omitempty"`
	MongoImage         string                   `json:"mongoImage,omitempty"`
	Message            string                   `json:"message,omitempty"`
	Conditions         []ClusterCondition       `json:"conditions,omitempty"`
	Replsets           map[string]ReplsetStatus `json:"replsets,omitempty"`
	Mongos             *MongosStatus            `json:"mongos,omitempty"`
	ObservedGeneration int64                    `json:"observedGeneration,omitempty"`
	BackupStatus       AppState                 `json:"backup,omitempty"`
	BackupVersion      string                   `json:"backupVersion,omitempty"`
	BackupConfigHash   string                   `json:"backupConfigHash,omitempty"`
	PMMStatus          AppState                 `json:"pmmStatus,omitempty"`
	PMMVersion         string                   `json:"pmmVersion,omitempty"`
	Host               string                   `json:"host,omitempty"`
	Size               int32                    `json:"size"`
	Ready              int32                    `json:"ready"`
}

type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

type ClusterCondition struct {
	Status             ConditionStatus `json:"status"`
	Type               AppState        `json:"type"`
	LastTransitionTime metav1.Time     `json:"lastTransitionTime,omitempty"`
	Reason             string          `json:"reason,omitempty"`
	Message            string          `json:"message,omitempty"`
}

// FindCondition finds the conditionType in conditions.
func (s *PerconaServerMongoDBStatus) FindCondition(conditionType AppState) *ClusterCondition {
	for i, c := range s.Conditions {
		if c.Type == conditionType {
			return &s.Conditions[i]
		}
	}
	return nil
}

type PMMSpec struct {
	Enabled                  bool                    `json:"enabled,omitempty"`
	ServerHost               string                  `json:"serverHost,omitempty"`
	Image                    string                  `json:"image"`
	MongodParams             string                  `json:"mongodParams,omitempty"`
	MongosParams             string                  `json:"mongosParams,omitempty"`
	ContainerSecurityContext *corev1.SecurityContext `json:"containerSecurityContext,omitempty"`

	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// PMM cluster name. If not set Operator uses cr.Name for PMM cluster name.
	CustomClusterName string `json:"customClusterName,omitempty"`
}

// HasSecret is used for PMM2. PMM2 is reaching its EOL.
func (pmm *PMMSpec) HasSecret(secret *corev1.Secret) bool {
	if len(secret.Data) == 0 {
		return false
	}
	s := sets.StringKeySet(secret.Data)
	if s.HasAll(PMMUserKey, PMMPasswordKey) || s.Has(PMMAPIKey) {
		return true
	}
	return false
}

// ShouldUseAPIKeyAuth is used for PMM2. PMM2 is reaching its EOL.
func (spec *PMMSpec) ShouldUseAPIKeyAuth(secret *corev1.Secret) bool {
	if _, ok := secret.Data[PMMAPIKey]; !ok {
		_, okl := secret.Data[PMMUserKey]
		_, okp := secret.Data[PMMPasswordKey]
		if okl && okp {
			return false
		}
	}
	return true
}

type MultiAZ struct {
	Affinity                      *PodAffinity                      `json:"affinity,omitempty"`
	TopologySpreadConstraints     []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
	NodeSelector                  map[string]string                 `json:"nodeSelector,omitempty"`
	Tolerations                   []corev1.Toleration               `json:"tolerations,omitempty"`
	PriorityClassName             string                            `json:"priorityClassName,omitempty"`
	ServiceAccountName            string                            `json:"serviceAccountName,omitempty"`
	Annotations                   map[string]string                 `json:"annotations,omitempty"`
	Labels                        map[string]string                 `json:"labels,omitempty"`
	PodDisruptionBudget           *PodDisruptionBudgetSpec          `json:"podDisruptionBudget,omitempty"`
	TerminationGracePeriodSeconds *int64                            `json:"terminationGracePeriodSeconds,omitempty"`
	RuntimeClassName              *string                           `json:"runtimeClassName,omitempty"`

	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	Sidecars       []corev1.Container             `json:"sidecars,omitempty"`
	SidecarVolumes []corev1.Volume                `json:"sidecarVolumes,omitempty"`
	SidecarPVCs    []corev1.PersistentVolumeClaim `json:"sidecarPVCs,omitempty"`
}

func (m *MultiAZ) WithSidecars(c corev1.Container) (withSidecars []corev1.Container, noSkips bool) {
	withSidecars, noSkips = []corev1.Container{c}, true

	for _, s := range m.Sidecars {
		if s.Name == c.Name {
			noSkips = false
			continue
		}

		withSidecars = append(withSidecars, s)
	}

	return
}

func (m *MultiAZ) WithSidecarVolumes(log logr.Logger, volumes []corev1.Volume) []corev1.Volume {
	names := make(map[string]struct{}, len(volumes))
	for i := range volumes {
		names[volumes[i].Name] = struct{}{}
	}

	rv := make([]corev1.Volume, 0, len(volumes)+len(m.SidecarVolumes))
	rv = append(rv, volumes...)

	for _, v := range m.SidecarVolumes {
		if _, ok := names[v.Name]; ok {
			log.Error(errors.New("Wrong sidecar volume name, it is skipped"), "volumeName", v.Name)
			continue
		}

		rv = append(rv, v)
	}

	return rv
}

func (m *MultiAZ) WithSidecarPVCs(log logr.Logger, pvcs []corev1.PersistentVolumeClaim) []corev1.PersistentVolumeClaim {
	names := make(map[string]struct{}, len(pvcs))
	for i := range pvcs {
		names[pvcs[i].Name] = struct{}{}
	}

	rv := make([]corev1.PersistentVolumeClaim, 0, len(pvcs)+len(m.SidecarPVCs))
	rv = append(rv, pvcs...)

	for _, p := range m.SidecarPVCs {
		if _, ok := names[p.Name]; ok {
			log.Error(errors.New("Wrong sidecar PVC name, it is skipped"), "PVCName", p.Name)
			continue
		}

		rv = append(rv, p)
	}

	return rv
}

type PodDisruptionBudgetSpec struct {
	MinAvailable   *intstr.IntOrString `json:"minAvailable,omitempty"`
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
}

type PodAffinity struct {
	TopologyKey *string          `json:"antiAffinityTopologyKey,omitempty"`
	Advanced    *corev1.Affinity `json:"advanced,omitempty"`
}

type ExternalNode struct {
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`
	Priority int    `json:"priority"`
	Votes    int    `json:"votes"`

	ReplsetOverride `json:",inline"`
}

func (e *ExternalNode) HostPort() string {
	return e.Host + ":" + strconv.Itoa(e.Port)
}

type NonVotingSpec struct {
	Enabled                  bool                       `json:"enabled"`
	Size                     int32                      `json:"size"`
	VolumeSpec               *VolumeSpec                `json:"volumeSpec,omitempty"`
	ReadinessProbe           *corev1.Probe              `json:"readinessProbe,omitempty"`
	LivenessProbe            *LivenessProbeExtended     `json:"livenessProbe,omitempty"`
	PodSecurityContext       *corev1.PodSecurityContext `json:"podSecurityContext,omitempty"`
	ContainerSecurityContext *corev1.SecurityContext    `json:"containerSecurityContext,omitempty"`
	Configuration            MongoConfiguration         `json:"configuration,omitempty"`

	MultiAZ `json:",inline"`
}

func (nv *NonVotingSpec) GetSize() int32 {
	if !nv.Enabled {
		return 0
	}
	return nv.Size
}

type HiddenSpec struct {
	Enabled                  bool                       `json:"enabled"`
	Size                     int32                      `json:"size"`
	VolumeSpec               *VolumeSpec                `json:"volumeSpec,omitempty"`
	ReadinessProbe           *corev1.Probe              `json:"readinessProbe,omitempty"`
	LivenessProbe            *LivenessProbeExtended     `json:"livenessProbe,omitempty"`
	PodSecurityContext       *corev1.PodSecurityContext `json:"podSecurityContext,omitempty"`
	ContainerSecurityContext *corev1.SecurityContext    `json:"containerSecurityContext,omitempty"`
	Configuration            MongoConfiguration         `json:"configuration,omitempty"`

	MultiAZ `json:",inline"`
}

func (h *HiddenSpec) GetSize() int32 {
	if !h.Enabled {
		return 0
	}
	return h.Size
}

type MongoConfiguration string

func (conf MongoConfiguration) GetOptions(name string) (map[interface{}]interface{}, error) {
	m := make(map[string]interface{})
	err := yaml.Unmarshal([]byte(conf), m)
	if err != nil {
		return nil, err
	}
	val, ok := m[name]
	if !ok {
		return nil, nil
	}
	options, _ := val.(map[interface{}]interface{})
	return options, nil
}

func (conf MongoConfiguration) GetTLSMode() (string, error) {
	m, err := conf.GetOptions("net")
	if err != nil || m == nil {
		return "", err
	}

	tls, ok := m["tls"]
	if !ok {
		return "", nil
	}

	tlsMap, ok := tls.(map[any]any)
	if !ok {
		return "", errors.New("tls configuration is invalid")
	}

	tlsMode, ok := tlsMap["mode"]
	if !ok {
		return "", nil
	}

	mode, ok := tlsMode.(string)
	if !ok {
		return "", errors.Errorf("can't cast %s to string", mode)
	}

	return mode, nil
}

// isEncryptionEnabled returns nil if "enableEncryption" field is not specified or the pointer to the value of this field
func (conf MongoConfiguration) isEncryptionEnabled() (*bool, error) {
	m, err := conf.GetOptions("security")
	if err != nil || m == nil {
		return nil, err
	}
	enabled, ok := m["enableEncryption"]
	if !ok {
		return nil, nil
	}
	b, ok := enabled.(bool)
	if !ok {
		return nil, errors.New("enableEncryption value is not bool")
	}
	return &b, nil
}

// VaultEnabled returns whether mongo config has vault section under security
func (conf MongoConfiguration) VaultEnabled() bool {
	m, err := conf.GetOptions("security")
	if err != nil || m == nil {
		return false
	}
	_, ok := m["vault"]
	return ok
}

// QuietEnabled returns whether mongo config has `quiet` set to true under `systemLog` section.
// If `quiet` or `systemLog` sections are not present, returns true.
func (conf MongoConfiguration) QuietEnabled() bool {
	defaultValue := true

	m, err := conf.GetOptions("systemLog")
	if err != nil || m == nil {
		return defaultValue
	}
	v, ok := m["quiet"]
	if !ok {
		return defaultValue
	}
	b, ok := v.(bool)
	if !ok {
		return defaultValue
	}

	return b
}

// GetPort returns the net.port of the mongo configuration.
// https://www.mongodb.com/docs/manual/reference/configuration-options/#mongodb-setting-net.port
func (conf MongoConfiguration) GetPort() (int32, error) {
	var cfg struct {
		Net struct {
			Port int32 `yaml:"port,omitempty"`
		} `yaml:"net,omitempty"`
	}
	err := yaml.Unmarshal([]byte(conf), &cfg)
	if err != nil {
		return 0, fmt.Errorf("error unmarshalling configuration %v", err)
	}
	return cfg.Net.Port, nil
}

// SetPort to set the mongo port in the MongoConfiguration according to the following documentation:
// https://www.mongodb.com/docs/manual/reference/configuration-options/#mongodb-setting-net.port.
// Caution using this since it overwrites the MongoConfiguration.
func (conf *MongoConfiguration) SetPort(port int32) error {
	if *conf != "" {
		return fmt.Errorf("configuration is not empty; refusing to overwrite")
	}
	var cfg struct {
		Net struct {
			Port int32 `yaml:"port,omitempty"`
		} `yaml:"net,omitempty"`
	}

	err := yaml.Unmarshal([]byte(*conf), &cfg)
	if err != nil {
		return fmt.Errorf("error unmarshalling configuration %v", err)
	}

	cfg.Net.Port = port

	newConfig, _ := yaml.Marshal(cfg)

	*conf = MongoConfiguration(newConfig)

	return nil
}

// setEncryptionDefaults sets encryptionKeyFile to a default value if enableEncryption is specified.
func (conf *MongoConfiguration) setEncryptionDefaults() error {
	m := make(map[string]interface{})

	err := yaml.Unmarshal([]byte(*conf), m)
	if err != nil {
		return err
	}

	val, ok := m["security"]
	if !ok {
		return nil
	}

	security, ok := val.(map[interface{}]interface{})
	if !ok {
		return errors.New("security configuration section is invalid")
	}

	if _, ok := security["vault"]; ok {
		return nil
	}

	if _, ok = security["enableEncryption"]; ok {
		security["encryptionKeyFile"] = MongodRESTencryptDir + "/" + EncryptionKeyName
	}

	res, err := yaml.Marshal(m)
	if err != nil {
		return err
	}

	*conf = MongoConfiguration(res)

	return nil
}

func (conf *MongoConfiguration) SetDefaults() error {
	if err := conf.setEncryptionDefaults(); err != nil {
		return errors.Wrap(err, "failed to set encryption defaults")
	}
	return nil
}

type ReplsetOverrides map[string]ReplsetOverride

type ReplsetOverride struct {
	Host     string            `json:"host,omitempty"`
	Horizons map[string]string `json:"horizons,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
	Priority *int              `json:"priority,omitempty"`
}

type HorizonsSpec map[string]map[string]string

type PrimaryPreferTagSelectorSpec map[string]string

type ReplsetSpec struct {
	MultiAZ `json:",inline"`

	Name                     string                       `json:"name,omitempty"`
	Size                     int32                        `json:"size"`
	ClusterRole              ClusterRole                  `json:"clusterRole,omitempty"`
	Arbiter                  Arbiter                      `json:"arbiter,omitempty"`
	Expose                   ExposeTogglable              `json:"expose,omitempty"`
	VolumeSpec               *VolumeSpec                  `json:"volumeSpec,omitempty"`
	ReadinessProbe           *corev1.Probe                `json:"readinessProbe,omitempty"`
	LivenessProbe            *LivenessProbeExtended       `json:"livenessProbe,omitempty"`
	PodSecurityContext       *corev1.PodSecurityContext   `json:"podSecurityContext,omitempty"`
	ContainerSecurityContext *corev1.SecurityContext      `json:"containerSecurityContext,omitempty"`
	Storage                  *MongodSpecStorage           `json:"storage,omitempty"`
	Configuration            MongoConfiguration           `json:"configuration,omitempty"`
	ExternalNodes            []*ExternalNode              `json:"externalNodes,omitempty"`
	NonVoting                NonVotingSpec                `json:"nonvoting,omitempty"`
	Hidden                   HiddenSpec                   `json:"hidden,omitempty"`
	HostAliases              []corev1.HostAlias           `json:"hostAliases,omitempty"`
	Horizons                 HorizonsSpec                 `json:"splitHorizons,omitempty"`
	ReplsetOverrides         ReplsetOverrides             `json:"replsetOverrides,omitempty"`
	PrimaryPreferTagSelector PrimaryPreferTagSelectorSpec `json:"primaryPreferTagSelector,omitempty"`
}

func (r *ReplsetSpec) PodName(cr *PerconaServerMongoDB, idx int) string {
	return fmt.Sprintf("%s-%s-%d", cr.Name, r.Name, idx)
}

func (r ReplsetSpec) CustomReplsetName() (string, error) {
	var cfg struct {
		Replication struct {
			ReplSetName string `yaml:"replSetName,omitempty"`
		} `yaml:"replication,omitempty"`
	}

	err := yaml.Unmarshal([]byte(r.Configuration), &cfg)
	if err != nil {
		return cfg.Replication.ReplSetName, errors.Wrap(err, "unmarshal configuration")
	}

	if len(cfg.Replication.ReplSetName) == 0 {
		return cfg.Replication.ReplSetName, errors.New("replSetName is not configured")
	}

	return cfg.Replication.ReplSetName, nil
}

func (ms ReplsetSpec) GetPort() int32 {
	if p, err := ms.Configuration.GetPort(); err == nil && p > 0 {
		return p
	}
	return DefaultMongoPort
}

func (r ReplsetSpec) GetSize() int32 {
	return r.Size + r.Arbiter.GetSize() + r.NonVoting.GetSize() + r.Hidden.GetSize()
}

type LivenessProbeExtended struct {
	corev1.Probe        `json:",inline"`
	StartupDelaySeconds int `json:"startupDelaySeconds,omitempty"`
}

func (l LivenessProbeExtended) CommandHas(flag string) bool {
	if l.ProbeHandler.Exec == nil {
		return false
	}

	for _, v := range l.ProbeHandler.Exec.Command {
		if v == flag {
			return true
		}
	}

	return false
}

type VolumeSpec struct {
	// EmptyDir represents a temporary directory that shares a pod's lifetime.
	EmptyDir *corev1.EmptyDirVolumeSource `json:"emptyDir,omitempty"`

	// HostPath represents a pre-existing file or directory on the host machine
	// that is directly exposed to the container.
	HostPath *corev1.HostPathVolumeSource `json:"hostPath,omitempty"`

	// PersistentVolumeClaim represents a reference to a PersistentVolumeClaim.
	// It has the highest level of precedence, followed by HostPath and
	// EmptyDir. And represents the PVC specification.
	PersistentVolumeClaim PVCSpec `json:"persistentVolumeClaim,omitempty"`
}

type PVCSpec struct {
	Annotations map[string]string `json:"annotations,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`

	*corev1.PersistentVolumeClaimSpec `json:",inline"`
}

type SecretsSpec struct {
	Users       string `json:"users,omitempty"`
	SSL         string `json:"ssl,omitempty"`
	SSLInternal string `json:"sslInternal,omitempty"`

	// Use (*SecretsSpec) GetInternalKey() to get InternalKey
	InternalKey string `json:"keyFile,omitempty"`

	EncryptionKey string `json:"encryptionKey,omitempty"`
	Vault         string `json:"vault,omitempty"`
	SSE           string `json:"sse,omitempty"`
	LDAPSecret    string `json:"ldapSecret,omitempty"`
}

func (s *SecretsSpec) GetInternalKey(cr *PerconaServerMongoDB) string {
	if s == nil || s.InternalKey == "" {
		return cr.Name + "-mongodb-keyfile"
	}
	return s.InternalKey
}

func SSLSecretName(cr *PerconaServerMongoDB) string {
	return cr.Spec.Secrets.SSL
}

func SSLInternalSecretName(cr *PerconaServerMongoDB) string {
	return cr.Spec.Secrets.SSLInternal
}

type MongosSpec struct {
	MultiAZ `json:",inline"`

	Port                     int32                      `json:"port,omitempty"`
	HostPort                 int32                      `json:"hostPort,omitempty"`
	SetParameter             *MongosSpecSetParameter    `json:"setParameter,omitempty"`
	Expose                   MongosExpose               `json:"expose,omitempty"`
	Size                     int32                      `json:"size,omitempty"`
	ReadinessProbe           *corev1.Probe              `json:"readinessProbe,omitempty"`
	LivenessProbe            *LivenessProbeExtended     `json:"livenessProbe,omitempty"`
	PodSecurityContext       *corev1.PodSecurityContext `json:"podSecurityContext,omitempty"`
	ContainerSecurityContext *corev1.SecurityContext    `json:"containerSecurityContext,omitempty"`
	Configuration            MongoConfiguration         `json:"configuration,omitempty"`
	HostAliases              []corev1.HostAlias         `json:"hostAliases,omitempty"`
}

func (ms MongosSpec) GetPort() int32 {
	if p, err := ms.Configuration.GetPort(); err == nil && p > 0 {
		return p
	}

	if ms.Port != 0 {
		return ms.Port
	}

	return DefaultMongoPort
}

type MongosSpecSetParameter struct {
	CursorTimeoutMillis int `json:"cursorTimeoutMillis,omitempty"`
}

type StorageEngine string

var (
	StorageEngineWiredTiger StorageEngine = "wiredTiger"
	StorageEngineInMemory   StorageEngine = "inMemory"
	StorageEngineMMAPv1     StorageEngine = "mmapv1"
)

type MongodSpecStorage struct {
	Engine         StorageEngine         `json:"engine,omitempty"`
	DirectoryPerDB bool                  `json:"directoryPerDB,omitempty"`
	SyncPeriodSecs int                   `json:"syncPeriodSecs,omitempty"`
	InMemory       *MongodSpecInMemory   `json:"inMemory,omitempty"`
	MMAPv1         *MongodSpecMMAPv1     `json:"mmapv1,omitempty"`
	WiredTiger     *MongodSpecWiredTiger `json:"wiredTiger,omitempty"`
}

type MongodSpecMMAPv1 struct {
	NsSize     int  `json:"nsSize,omitempty"`
	Smallfiles bool `json:"smallfiles,omitempty"`
}

type WiredTigerCompressor string

var (
	WiredTigerCompressorNone   WiredTigerCompressor = "none"
	WiredTigerCompressorSnappy WiredTigerCompressor = "snappy"
	WiredTigerCompressorZlib   WiredTigerCompressor = "zlib"
)

type MongodSpecWiredTigerEngineConfig struct {
	CacheSizeRatio      numstr.NumberString   `json:"cacheSizeRatio,omitempty"`
	DirectoryForIndexes bool                  `json:"directoryForIndexes,omitempty"`
	JournalCompressor   *WiredTigerCompressor `json:"journalCompressor,omitempty"`
}

type MongodSpecWiredTigerCollectionConfig struct {
	BlockCompressor *WiredTigerCompressor `json:"blockCompressor,omitempty"`
}

type MongodSpecWiredTigerIndexConfig struct {
	PrefixCompression bool `json:"prefixCompression,omitempty"`
}

type MongodSpecWiredTiger struct {
	CollectionConfig *MongodSpecWiredTigerCollectionConfig `json:"collectionConfig,omitempty"`
	EngineConfig     *MongodSpecWiredTigerEngineConfig     `json:"engineConfig,omitempty"`
	IndexConfig      *MongodSpecWiredTigerIndexConfig      `json:"indexConfig,omitempty"`
}

type MongodSpecInMemoryEngineConfig struct {
	InMemorySizeRatio numstr.NumberString `json:"inMemorySizeRatio,omitempty"`
}

type MongodSpecInMemory struct {
	EngineConfig *MongodSpecInMemoryEngineConfig `json:"engineConfig,omitempty"`
}

type BackupTaskSpec struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Deprecated: Use Retention instead. This field will be removed in the future
	Keep int `json:"keep,omitempty"`
	// +optional
	Retention        *BackupTaskSpecRetention `json:"retention,omitempty"`
	Schedule         string                   `json:"schedule,omitempty"`
	StorageName      string                   `json:"storageName,omitempty"`
	CompressionType  compress.CompressionType `json:"compressionType,omitempty"`
	CompressionLevel *int                     `json:"compressionLevel,omitempty"`

	// +kubebuilder:validation:Enum={logical,physical,incremental,incremental-base}
	Type defs.BackupType `json:"type,omitempty"`
}

func (task *BackupTaskSpec) GetRetention(cr *PerconaServerMongoDB) BackupTaskSpecRetention {
	if task.Retention != nil && cr.CompareVersion("1.21.0") >= 0 {
		return *task.Retention
	}
	return BackupTaskSpecRetention{
		Type:              BackupTaskSpecRetentionTypeCount,
		Count:             task.Keep,
		DeleteFromStorage: true,
	}
}

type BackupTaskSpecRetentionType string

const (
	BackupTaskSpecRetentionTypeCount = "count"
)

type BackupTaskSpecRetention struct {
	// +kubebuilder:validation:Minimum=0
	Count int `json:"count,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum={count}
	Type string `json:"type,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:default=true
	DeleteFromStorage bool `json:"deleteFromStorage,omitempty"`
}

func (task *BackupTaskSpec) JobName(cr *PerconaServerMongoDB) string {
	return fmt.Sprintf("%s-backup-%s-%s", cr.Name, task.Name, cr.Namespace)
}

type S3ServiceSideEncryption struct {
	// Used to specify the SSE algorithm used when keys are managed by the server
	SSEAlgorithm string `json:"sseAlgorithm,omitempty"`
	KMSKeyID     string `json:"kmsKeyID,omitempty"`

	// Used to specify SSE-C style encryption. For Amazon S3 SSECustomerAlgorithm must be 'AES256'
	// see https://docs.aws.amazon.com/AmazonS3/latest/userguide/ServerSideEncryptionCustomerKeys.html
	SSECustomerAlgorithm string `json:"sseCustomerAlgorithm,omitempty"`

	// If SSECustomerAlgorithm is set, this must be a base64 encoded key compatible with the algorithm
	// specified in the SseCustomerAlgorithm field.
	SSECustomerKey string `json:"sseCustomerKey,omitempty"`
}

type Retryer struct {
	NumMaxRetries int             `json:"numMaxRetries,omitempty"`
	MinRetryDelay metav1.Duration `json:"minRetryDelay,omitempty"`
	MaxRetryDelay metav1.Duration `json:"maxRetryDelay,omitempty"`
}

type BackupStorageS3Spec struct {
	Bucket                string                  `json:"bucket"`
	Prefix                string                  `json:"prefix,omitempty"`
	Region                string                  `json:"region,omitempty"`
	EndpointURL           string                  `json:"endpointUrl,omitempty"`
	CredentialsSecret     string                  `json:"credentialsSecret,omitempty"`
	UploadPartSize        int                     `json:"uploadPartSize,omitempty"`
	MaxUploadParts        int                     `json:"maxUploadParts,omitempty"`
	StorageClass          string                  `json:"storageClass,omitempty"`
	InsecureSkipTLSVerify bool                    `json:"insecureSkipTLSVerify,omitempty"`
	ForcePathStyle        *bool                   `json:"forcePathStyle,omitempty"`
	DebugLogLevels        string                  `json:"debugLogLevels,omitempty"`
	Retryer               *Retryer                `json:"retryer,omitempty"`
	ServerSideEncryption  S3ServiceSideEncryption `json:"serverSideEncryption,omitempty"`
}

type BackupStorageAzureSpec struct {
	Container         string `json:"container,omitempty"`
	Prefix            string `json:"prefix,omitempty"`
	CredentialsSecret string `json:"credentialsSecret"`
	EndpointURL       string `json:"endpointUrl,omitempty"`
}

type BackupStorageFilesystemSpec struct {
	Path string `json:"path"`
}

type BackupStorageType string

const (
	BackupStorageFilesystem BackupStorageType = "filesystem"
	BackupStorageS3         BackupStorageType = "s3"
	BackupStorageAzure      BackupStorageType = "azure"
)

type BackupStorageSpec struct {
	Type       BackupStorageType           `json:"type"`
	Main       bool                        `json:"main,omitempty"`
	S3         BackupStorageS3Spec         `json:"s3,omitempty"`
	Azure      BackupStorageAzureSpec      `json:"azure,omitempty"`
	Filesystem BackupStorageFilesystemSpec `json:"filesystem,omitempty"`
}

type PITRSpec struct {
	Enabled          bool                     `json:"enabled,omitempty"`
	OplogSpanMin     numstr.NumberString      `json:"oplogSpanMin,omitempty"`
	OplogOnly        bool                     `json:"oplogOnly,omitempty"`
	CompressionType  compress.CompressionType `json:"compressionType,omitempty"`
	CompressionLevel *int                     `json:"compressionLevel,omitempty"`
}

type BackupTimeouts struct {
	Starting *uint32 `json:"startingStatus,omitempty"`
}

type BackupOptions struct {
	OplogSpanMin           float64            `json:"oplogSpanMin"`
	NumParallelCollections int                `json:"numParallelCollections,omitempty"`
	Priority               map[string]float64 `json:"priority,omitempty"`
	Timeouts               *BackupTimeouts    `json:"timeouts,omitempty"`
}

type RestoreOptions struct {
	BatchSize              int               `json:"batchSize,omitempty"`
	NumInsertionWorkers    int               `json:"numInsertionWorkers,omitempty"`
	NumDownloadWorkers     int               `json:"numDownloadWorkers,omitempty"`
	NumParallelCollections int               `json:"numParallelCollections,omitempty"`
	MaxDownloadBufferMb    int               `json:"maxDownloadBufferMb,omitempty"`
	DownloadChunkMb        int               `json:"downloadChunkMb,omitempty"`
	MongodLocation         string            `json:"mongodLocation,omitempty"`
	MongodLocationMap      map[string]string `json:"mongodLocationMap,omitempty"`
}

type BackupConfig struct {
	BackupOptions  *BackupOptions  `json:"backupOptions,omitempty"`
	RestoreOptions *RestoreOptions `json:"restoreOptions,omitempty"`
}

type BackupSpec struct {
	Enabled                  bool                         `json:"enabled"`
	Annotations              map[string]string            `json:"annotations,omitempty"`
	Labels                   map[string]string            `json:"labels,omitempty"`
	Storages                 map[string]BackupStorageSpec `json:"storages,omitempty"`
	Image                    string                       `json:"image"`
	Tasks                    []BackupTaskSpec             `json:"tasks,omitempty"`
	ServiceAccountName       string                       `json:"serviceAccountName,omitempty"`
	PodSecurityContext       *corev1.PodSecurityContext   `json:"podSecurityContext,omitempty"`
	ContainerSecurityContext *corev1.SecurityContext      `json:"containerSecurityContext,omitempty"`
	Resources                corev1.ResourceRequirements  `json:"resources,omitempty"`
	RuntimeClassName         *string                      `json:"runtimeClassName,omitempty"`
	PITR                     PITRSpec                     `json:"pitr,omitempty"`
	Configuration            BackupConfig                 `json:"configuration,omitempty"`
	VolumeMounts             []corev1.VolumeMount         `json:"volumeMounts,omitempty"`
	StartingDeadlineSeconds  *int64                       `json:"startingDeadlineSeconds,omitempty"`
}

func (b BackupSpec) IsPITREnabled() bool {
	if !b.Enabled {
		return false
	}
	if len(b.Storages) != 1 {
		return false
	}
	return b.PITR.Enabled
}

var ErrNoMainStorage = errors.New("main storage not found")

func (b BackupSpec) MainStorage() (string, BackupStorageSpec, error) {
	if len(b.Storages) == 1 {
		for name, stg := range b.Storages {
			return name, stg, nil
		}
	}

	for name, stg := range b.Storages {
		if !stg.Main {
			continue
		}

		return name, stg, nil
	}

	return "", BackupStorageSpec{}, ErrNoMainStorage
}

type Arbiter struct {
	MultiAZ `json:",inline"`

	Enabled bool  `json:"enabled"`
	Size    int32 `json:"size"`
}

func (a *Arbiter) GetSize() int32 {
	if !a.Enabled {
		return 0
	}
	return a.Size
}

type MongosExpose struct {
	ServicePerPod bool  `json:"servicePerPod,omitempty"`
	NodePort      int32 `json:"nodePort,omitempty"`

	Expose `json:",inline"`
}

type ExposeTogglable struct {
	Enabled bool `json:"enabled"`

	Expose `json:",inline"`
}

type Expose struct {
	ExposeType           corev1.ServiceType `json:"type,omitempty"`
	DeprecatedExposeType corev1.ServiceType `json:"exposeType,omitempty"`

	LoadBalancerSourceRanges []string `json:"loadBalancerSourceRanges,omitempty"`

	// LoadBalancerClass is the class of the load balancer implementation the Service belongs to.
	// This field can only be set when the Service type is 'LoadBalancer'.
	// This field can only be set when creating or updating a Service to type 'LoadBalancer'.
	// Once set, it can not be changed.
	LoadBalancerClass *string `json:"loadBalancerClass,omitempty"`

	ServiceAnnotations           map[string]string `json:"annotations,omitempty"`
	DeprecatedServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`

	ServiceLabels           map[string]string `json:"labels,omitempty"`
	DeprecatedServiceLabels map[string]string `json:"serviceLabels,omitempty"`

	InternalTrafficPolicy *corev1.ServiceInternalTrafficPolicy `json:"internalTrafficPolicy,omitempty"`
	ExternalTrafficPolicy corev1.ServiceExternalTrafficPolicy  `json:"externalTrafficPolicy,omitempty"`
}

func (e *Expose) SaveOldMeta() bool {
	return len(e.ServiceAnnotations) == 0 && len(e.ServiceLabels) == 0
}

// ServerVersion represents info about k8s / openshift server version
type ServerVersion struct {
	Platform version.Platform
	Info     k8sversion.Info
}

// OwnerRef returns OwnerReference to object
func (cr *PerconaServerMongoDB) OwnerRef(scheme *runtime.Scheme) (metav1.OwnerReference, error) {
	gvk, err := apiutil.GVKForObject(cr, scheme)
	if err != nil {
		return metav1.OwnerReference{}, err
	}

	trueVar := true

	return metav1.OwnerReference{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Name:       cr.GetName(),
		UID:        cr.GetUID(),
		Controller: &trueVar,
	}, nil
}

func (cr *PerconaServerMongoDB) Version() *v.Version {
	return v.Must(v.NewVersion(cr.Spec.CRVersion))
}

func (cr *PerconaServerMongoDB) CompareVersion(version string) int {
	// using Must because "version" must be right format
	return cr.Version().Compare(v.Must(v.NewVersion(version)))
}

func (cr *PerconaServerMongoDB) CompareMongoDBVersion(version string) (int, error) {
	mongoVer, err := v.NewVersion(cr.Status.MongoVersion)
	if err != nil {
		return 0, errors.Wrap(err, "parse status.mongoVersion")
	}

	compare, err := v.NewVersion(version)
	if err != nil {
		return 0, errors.Wrap(err, "parse version")
	}

	return mongoVer.Compare(compare), nil
}

const (
	internalPrefix = "internal-"
	userPostfix    = "-users"
)

const (
	PMMUserKey     = "PMM_SERVER_USER"
	PMMPasswordKey = "PMM_SERVER_PASSWORD"
	PMMAPIKey      = "PMM_SERVER_API_KEY"
	PMMServerToken = "PMM_SERVER_TOKEN"
)

const (
	EnvMongoDBDatabaseAdminUser      = "MONGODB_DATABASE_ADMIN_USER"
	EnvMongoDBDatabaseAdminPassword  = "MONGODB_DATABASE_ADMIN_PASSWORD"
	EnvMongoDBClusterAdminUser       = "MONGODB_CLUSTER_ADMIN_USER"
	EnvMongoDBClusterAdminPassword   = "MONGODB_CLUSTER_ADMIN_PASSWORD"
	EnvMongoDBUserAdminUser          = "MONGODB_USER_ADMIN_USER"
	EnvMongoDBUserAdminPassword      = "MONGODB_USER_ADMIN_PASSWORD"
	EnvMongoDBBackupUser             = "MONGODB_BACKUP_USER"
	EnvMongoDBBackupPassword         = "MONGODB_BACKUP_PASSWORD"
	EnvMongoDBClusterMonitorUser     = "MONGODB_CLUSTER_MONITOR_USER"
	EnvMongoDBClusterMonitorPassword = "MONGODB_CLUSTER_MONITOR_PASSWORD"
	EnvPMMServerUser                 = PMMUserKey
	EnvPMMServerPassword             = PMMPasswordKey
	EnvPMMServerAPIKey               = PMMAPIKey
)

type SystemUserRole string

const (
	RoleDatabaseAdmin  SystemUserRole = "databaseAdmin"
	RoleClusterAdmin   SystemUserRole = "clusterAdmin"
	RoleUserAdmin      SystemUserRole = "userAdmin"
	RoleClusterMonitor SystemUserRole = "clusterMonitor"
	RoleBackup         SystemUserRole = "backup"
)

func InternalUserSecretName(cr *PerconaServerMongoDB) string {
	return internalPrefix + cr.Name + userPostfix
}

func UserSecretName(cr *PerconaServerMongoDB) string {
	name := cr.Spec.Secrets.Users
	if cr.CompareVersion("1.5.0") >= 0 {
		name = InternalUserSecretName(cr)
	}

	return name
}

func (cr *PerconaServerMongoDB) NamespacedName() types.NamespacedName {
	return types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}
}

func (cr *PerconaServerMongoDB) StatefulsetNamespacedName(rsName string) types.NamespacedName {
	return types.NamespacedName{Name: cr.Name + "-" + rsName, Namespace: cr.Namespace}
}

func (cr *PerconaServerMongoDB) MongosNamespacedName() types.NamespacedName {
	return types.NamespacedName{Name: cr.Name + "-" + "mongos", Namespace: cr.Namespace}
}

func (cr *PerconaServerMongoDB) CanBackup(ctx context.Context) error {
	log := logf.FromContext(ctx).V(1).WithValues("cluster", cr.Name, "namespace", cr.Namespace)
	log.Info("checking if backup is allowed")

	if cr.Status.State == AppStateReady {
		return nil
	}

	if cr.CompareVersion("1.15.0") <= 0 && !cr.Spec.UnsafeConf {
		return errors.Errorf("allowUnsafeConfigurations must be true to run backup on cluster with status %s", cr.Status.State)
	}

	if cr.CompareVersion("1.16.0") >= 0 && !cr.Spec.Unsafe.BackupIfUnhealthy {
		return errors.Errorf("spec.unsafeFlags.backupIfUnhealthy must be true to run backup on cluster with status %s", cr.Status.State)
	}

	for rsName, rs := range cr.Status.Replsets {
		if rs.Ready < int32(1) {
			return errors.New(rsName + " has no ready nodes")
		}
	}

	return nil
}

func (cr *PerconaServerMongoDB) CanRestore(ctx context.Context) error {
	log := logf.FromContext(ctx).V(1).WithValues("cluster", cr.Name, "namespace", cr.Namespace)
	log.Info("checking if restore is allowed")

	if cr.Spec.Unmanaged {
		return errors.New("can't run restore in an unmanaged cluster")
	}

	return nil
}

func (s *PerconaServerMongoDBStatus) AddCondition(c ClusterCondition) {
	existingCondition := s.FindCondition(c.Type)
	if existingCondition == nil {
		if c.LastTransitionTime.IsZero() {
			c.LastTransitionTime = metav1.NewTime(time.Now())
		}
		s.Conditions = append(s.Conditions, c)
		return
	}

	if existingCondition.Status != c.Status {
		existingCondition.Status = c.Status
		if !c.LastTransitionTime.IsZero() {
			existingCondition.LastTransitionTime = c.LastTransitionTime
		} else {
			existingCondition.LastTransitionTime = metav1.NewTime(time.Now())
		}
	}

	if existingCondition.Reason != c.Reason {
		existingCondition.Reason = c.Reason
	}
	if existingCondition.Message != c.Message {
		existingCondition.Message = c.Message
	}
}

// GetExternalNodes returns all external nodes for all replsets
func (cr *PerconaServerMongoDB) GetExternalNodes() []*ExternalNode {
	extNodes := make([]*ExternalNode, 0)

	for _, replset := range cr.Spec.Replsets {
		extNodes = append(extNodes, replset.ExternalNodes...)
	}

	return extNodes
}

func (cr *PerconaServerMongoDB) MCSEnabled() bool {
	return mcs.IsAvailable() && cr.Spec.MultiCluster.Enabled
}

func (cr *PerconaServerMongoDB) TLSEnabled() bool {
	if cr.CompareVersion("1.16.0") < 0 {
		return !cr.Spec.UnsafeConf
	}

	if cr.Spec.TLS != nil {
		switch cr.Spec.TLS.Mode {
		case TLSModeDisabled:
			return false
		case TLSModeAllow, TLSModePrefer, TLSModeRequire:
			return true
		}
	}

	return true
}

func (cr *PerconaServerMongoDB) UnsafeTLSDisabled() bool {
	return (cr.CompareVersion("1.16.0") >= 0 && cr.Spec.Unsafe.TLS) || (cr.CompareVersion("1.16.0") < 0 && cr.Spec.UnsafeConf)
}

const (
	AnnotationResyncPBM           = "percona.com/resync-pbm"
	AnnotationResyncInProgress    = "percona.com/resync-in-progress"
	AnnotationPVCResizeInProgress = "percona.com/pvc-resize-in-progress"
)

func (cr *PerconaServerMongoDB) PBMResyncNeeded() bool {
	v, ok := cr.Annotations[AnnotationResyncPBM]
	return ok && v != ""
}

func (cr *PerconaServerMongoDB) PBMResyncInProgress() bool {
	v, ok := cr.Annotations[AnnotationResyncInProgress]
	return ok && v != ""
}

// LogCollectorSpec defines the configuration for enabling and customizing
// the log collection component that stores logs in a PVC.
type LogCollectorSpec struct {
	Enabled                  bool                        `json:"enabled,omitempty"`
	Image                    string                      `json:"image,omitempty"`
	Resources                corev1.ResourceRequirements `json:"resources,omitempty"`
	Configuration            string                      `json:"configuration,omitempty"`
	ContainerSecurityContext *corev1.SecurityContext     `json:"containerSecurityContext,omitempty"`
	ImagePullPolicy          corev1.PullPolicy           `json:"imagePullPolicy,omitempty"`
}

func (cr *PerconaServerMongoDB) IsLogCollectorEnabled() bool {
	return cr.Spec.LogCollector != nil && cr.Spec.LogCollector.Enabled
}
