package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sversion "k8s.io/apimachinery/pkg/version"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	"github.com/Percona-Lab/percona-server-mongodb-operator/version"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerconaServerMongoDB is the Schema for the perconaservermongodbs API
// +k8s:openapi-gen=true
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
	Platform        *version.Platform `json:"platform,omitempty"`
	Version         string            `json:"version,omitempty"`
	RunUID          int64             `json:"runUid,omitempty"`
	Mongod          *MongodSpec       `json:"mongod,omitempty"`
	Replsets        []*ReplsetSpec    `json:"replsets,omitempty"`
	Secrets         *SecretsSpec      `json:"secrets,omitempty"`
	Backup          *BackupSpec       `json:"backup,omitempty"`
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`
}

// PerconaServerMongoDBStatus defines the observed state of PerconaServerMongoDB
type PerconaServerMongoDBStatus struct {
	Replsets map[string]*ReplsetStatus `json:"replsets,omitempty"`
}

type ReplsetSpec struct {
	Resources    *ResourcesSpec `json:"resources,omitempty"`
	Name         string         `json:"name"`
	Size         int32          `json:"size"`
	StorageClass string         `json:"storageClass,omitempty"`
	ClusterRole  ClusterRole    `json:"clusterRole,omitempty"`
	Arbiter      *Arbiter       `json:"arbiter,omitempty"`
	Expose       *Expose        `json:"expose,omitempty"`
}

type ResourceSpecRequirements struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

type ResourcesSpec struct {
	Limits   *ResourceSpecRequirements `json:"limits,omitempty"`
	Requests *ResourceSpecRequirements `json:"requests,omitempty"`
	Storage  string                    `json:"storage,omitempty"`
}

type SecretsSpec struct {
	Users string `json:"users,omitempty"`
}

type ReplsetMemberStatus struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type ReplsetStatus struct {
	Name        string                 `json:"name,omitempty"`
	Pods        []string               `json:"pods,omitempty"`
	Members     []*ReplsetMemberStatus `json:"members,omitempty"`
	ClusterRole ClusterRole            `json:"clusterRole,omitempty"`
	Initialized bool                   `json:"initialized,omitempty"`
}

type MongosSpec struct {
	*ResourcesSpec `json:"resources,omitempty"`
	Port           int32 `json:"port,omitempty"`
	HostPort       int32 `json:"hostPort,omitempty"`
}

type MongodSpec struct {
	Net                *MongodSpecNet                `json:"net,omitempty"`
	AuditLog           *MongodSpecAuditLog           `json:"auditLog,omitempty"`
	OperationProfiling *MongodSpecOperationProfiling `json:"operationProfiling,omitempty"`
	Replication        *MongodSpecReplication        `json:"replication,omitempty"`
	Security           *MongodSpecSecurity           `json:"security,omitempty"`
	SetParameter       *MongodSpecSetParameter       `json:"setParameter,omitempty"`
	Storage            *MongodSpecStorage            `json:"storage,omitempty"`
}

type MongodSpecNet struct {
	Port     int32 `json:"port,omitempty"`
	HostPort int32 `json:"hostPort,omitempty"`
}

type MongodSpecReplication struct {
	OplogSizeMB int `json:"oplogSizeMB,omitempty"`
}

type MongodSpecSecurity struct {
	RedactClientLogData bool `json:"redactClientLogData,omitempty"`
}

type MongodSpecSetParameter struct {
	TTLMonitorSleepSecs                   int `json:"ttlMonitorSleepSecs,omitempty"`
	WiredTigerConcurrentReadTransactions  int `json:"wiredTigerConcurrentReadTransactions,omitempty"`
	WiredTigerConcurrentWriteTransactions int `json:"wiredTigerConcurrentWriteTransactions,omitempty"`
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
	CacheSizeRatio      float64               `json:"cacheSizeRatio,omitempty"`
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
	InMemorySizeRatio float64 `json:"inMemorySizeRatio,omitempty"`
}

type MongodSpecInMemory struct {
	EngineConfig *MongodSpecInMemoryEngineConfig `json:"engineConfig,omitempty"`
}

type AuditLogDestination string

var AuditLogDestinationFile AuditLogDestination = "file"

type AuditLogFormat string

var (
	AuditLogFormatBSON AuditLogFormat = "BSON"
	AuditLogFormatJSON AuditLogFormat = "JSON"
)

type MongodSpecAuditLog struct {
	Destination AuditLogDestination `json:"destination,omitempty"`
	Format      AuditLogFormat      `json:"format,omitempty"`
	Filter      string              `json:"filter,omitempty"`
}

type OperationProfilingMode string

const (
	OperationProfilingModeAll    OperationProfilingMode = "all"
	OperationProfilingModeSlowOp OperationProfilingMode = "slowOp"
)

type MongodSpecOperationProfiling struct {
	Mode              OperationProfilingMode `json:"mode,omitempty"`
	SlowOpThresholdMs int                    `json:"slowOpThresholdMs,omitempty"`
	RateLimit         int                    `json:"rateLimit,omitempty"`
}

type BackupCoordinatorSpec struct {
	*ResourcesSpec `json:"resources,omitempty"`
	Debug          bool `json:"debug,omitempty"`
}

type BackupS3Spec struct {
	Secret string `json:"secret,omitempty"`
	Bucket string `json:"bucket,omitempty"`
	Region string `json:"region,omitempty"`
}

type BackupDestinationType string

var (
	BackupDestinationS3   BackupDestinationType = "s3"
	BackupDestinationFile BackupDestinationType = "file"
)

type BackupTaskSpec struct {
	Name            string                `json:"name,omitempty"`
	Enabled         bool                  `json:"enabled"`
	Schedule        string                `json:"schedule,omitempty"`
	DestinationType BackupDestinationType `json:"destinationType,omitempty"`
}

type BackupSpec struct {
	Enabled          bool                   `json:"enabled"`
	Version          string                 `json:"version,omitempty"`
	RestartOnFailure *bool                  `json:"restartOnFailure,omitempty"`
	Coordinator      *BackupCoordinatorSpec `json:"coordinator,omitempty"`
	S3               *BackupS3Spec          `json:"s3,omitempty"`
	Tasks            []*BackupTaskSpec      `json:"tasks,omitempty"`
}

type Arbiter struct {
	Enabled bool  `json:"enabled"`
	Size    int32 `json:"size"`
}

type Expose struct {
	Enabled    bool               `json:"enabled"`
	ExposeType corev1.ServiceType `json:"exposeType,omitempty"`
}

type Platform string

const (
	PlatformUndef      Platform = ""
	PlatformKubernetes          = "kubernetes"
	PlatformOpenshift           = "openshift"
)

// ServerVersion represents info about k8s / openshift server version
type ServerVersion struct {
	Platform Platform
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
