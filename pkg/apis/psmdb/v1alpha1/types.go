package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type PerconaServerMongoDBList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`
	Items           []PerconaServerMongoDB `json:"items"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type PerconaServerMongoDB struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              PerconaServerMongoDBSpec   `json:"spec"`
	Status            PerconaServerMongoDBStatus `json:"status,omitempty"`
}

type PerconaServerMongoDBSpec struct {
	Version string       `json:"version,omitempty"`
	RunUID  int64        `json:"runUid,omitempty"`
	Mongod  *MongodSpec  `json:"mongod,omitempty"`
	Secrets *SecretsSpec `json:"secrets,omitempty"`
}

type PerconaServerMongoDBStatus struct {
	Replsets []*ReplsetStatus `json:"replsets,omitempty"`
}

type ResourceSpecRequirements struct {
	Cpu     string `json:"cpu,omitempty"`
	Memory  string `json:"memory,omitempty"`
	Storage string `json:"storage,omitempty"`
}

type ResourcesSpec struct {
	Limits   *ResourceSpecRequirements `json:"limits,omitempty"`
	Requests *ResourceSpecRequirements `json:"requests,omitempty"`
}

type SecretsSpec struct {
	Key   string `json:"key,omitempty"`
	Users string `json:"users,omitempty"`
}

type ReplsetSpec struct {
	Name string `json:"name"`
	Size int32  `json:"size"`
	//Mongod *MongodSpec `json:"mongod"`
}

type ReplsetStatus struct {
	Name        string   `json:"name,omitempty"`
	Pods        []string `json:"pods,omitempty"`
	Configsvr   bool     `json:"configsvr,omitempty"`
	Initialised bool     `json:"initialised,omitempty"`
}

type MongosSpec struct {
	*ResourcesSpec `json:"resources,omitempty"`
	Port           int32 `json:"port,omitempty"`
	HostPort       int32 `json:"hostPort,omitempty"`
}

type MongodSpec struct {
	*ResourcesSpec     `json:"resources,omitempty"`
	StorageClassName   string                        `json:"storageClassName,omitempty"`
	Net                *MongodSpecNet                `json:"net,omitempty"`
	Storage            *MongodSpecStorage            `json:"storage,omitempty"`
	OperationProfiling *MongodSpecOperationProfiling `json:"operationProfiling,omitempty"`
	AuditLog           *MongodSpecAuditLog           `json:"auditLog,omitempty"`
}

type ClusterRole string

const (
	ClusterRoleShardSvr  ClusterRole = "shardsvr"
	ClusterRoleConfigSvr ClusterRole = "configsvr"
)

type MongodSpecNet struct {
	Port     int32 `json:"port,omitempty"`
	HostPort int32 `json:"hostPort,omitempty"`
}

type StorageEngine string

var (
	StorageEngineWiredTiger StorageEngine = "wiredTiger"
	StorageEngineInMemory   StorageEngine = "inMemory"
	StorageEngineMMAPV1     StorageEngine = "mmapv1"
)

type MongodSpecStorage struct {
	Engine     StorageEngine         `json:"engine,omitempty"`
	InMemory   *MongodSpecInMemory   `json:"inMemory,omitempty"`
	MMAPv1     *MongodSpecMMAPv1     `json:"mmapv1,omitempty"`
	WiredTiger *MongodSpecWiredTiger `json:"wiredTiger,omitempty"`
}

type MongodSpecMMAPv1 struct {
	NsSize     int  `json:"nsSize,omitempty"`
	Smallfiles bool `json:"smallfiles,omitempty"`
}

type MongodSpecWiredTiger struct {
	CacheSizeRatio float64 `json:"cacheSizeRatio,omitempty"`
}

type MongodSpecInMemory struct {
	SizeRatio float64 `json:"sizeRatio,omitempty"`
}

type AuditLogDestination string

var (
	AuditLogDestinationFile AuditLogDestination = "file"
)

type AuditLogFormat string

var (
	AuditLogFormatBSON AuditLogFormat = "BSON"
	AuditLogFormatJSON AuditLogFormat = "JSON"
)

type MongodSpecAuditLog struct {
	Destination AuditLogDestination `json:"destination,omitempty"`
	Format      AuditLogFormat      `json:"format,omitempty"`
	Path        string              `json:"path,omitempty"`
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
}
