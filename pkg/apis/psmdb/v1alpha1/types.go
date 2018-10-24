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
	Version string      `json:"version,omitempty"`
	RunUID  int64       `json:"runUid,omitempty"`
	Mongod  *MongodSpec `json:"mongod,omitempty"`
	Secrets *Secrets    `json:"secrets,omitempty"`
}

type Secrets struct {
	Key   string `json:"key,omitempty"`
	Users string `json:"users,omitempty"`
}

type PerconaServerMongoDBStatus struct {
	Replsets []*ReplsetStatus `json:"replsets,omitempty"`
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

type OperationProfilingMode string

var (
	OperationProfilingModeAll    OperationProfilingMode = "all"
	OperationProfilingModeSlowOp OperationProfilingMode = "slowOp"
)

type MongodSpecOperationProfiling struct {
	Mode              OperationProfilingMode `json:"mode,omitempty"`
	SlowOpThresholdMs int                    `json:"slowMs,omitempty"`
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

type StorageEngine string

var (
	StorageEngineWiredTiger StorageEngine = "wiredTiger"
	StorageEngineInMemory   StorageEngine = "inMemory"
	StorageEngineMMAPV1     StorageEngine = "mmapv1"
)

type MongodSpec struct {
	*ResourcesSpec     `json:"resources,omitempty"`
	Size               int32                         `json:"size"`
	ReplsetName        string                        `json:"replsetName,omitempty"`
	Port               int32                         `json:"port,omitempty"`
	HostPort           int32                         `json:"hostPort,omitempty"`
	VolumeClassName    string                        `json:"volumeClassName,omitempty"`
	StorageEngine      StorageEngine                 `json:"storageEngine,omitempty"`
	InMemory           *MongodSpecInMemory           `json:"inMemory,omitempty"`
	MMAPv1             *MongodSpecMMAPv1             `json:"mmapv1,omitempty"`
	WiredTiger         *MongodSpecWiredTiger         `json:"wiredTiger,omitempty"`
	OperationProfiling *MongodSpecOperationProfiling `json:"operationProfiling,omitempty"`
}

type MongosSpec struct {
	*ResourcesSpec `json:"resources,omitempty"`
	Port           int32 `json:"port,omitempty"`
	HostPort       int32 `json:"hostPort,omitempty"`
}

type ReplsetStatus struct {
	Name        string   `json:"name,omitempty"`
	Members     []string `json:"members,omitempty"`
	Uri         string   `json:"uri,omitempty"`
	Configsvr   bool     `json:"configsvr,omitempty"`
	Initialised bool     `json:"initialised,omitempty"`
}
