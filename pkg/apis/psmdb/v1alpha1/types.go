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
	Size   int32       `json:"size"`
	Image  string      `json:"image,omitempty"`
	RunUID int64       `json:"runUid,omitempty"`
	Mongod *MongodSpec `json:"mongod,omitempty"`
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

type MongodSpecOperationProfiling struct {
	SlowMs int `json:"slowMs,omitempty"`
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

type MongodSpec struct {
	*ResourceSpecRequirements `json:"resources,omitempty"`
	Port                      int32                         `json:"port,omitempty"`
	HostPort                  int32                         `json:"hostPort,omitempty"`
	StorageEngine             string                        `json:"storageEngine,omitempty"`
	ReplsetName               string                        `json:"replsetName,omitempty"`
	MMAPv1                    *MongodSpecMMAPv1             `json:"mmapv1,omitempty"`
	WiredTiger                *MongodSpecWiredTiger         `json:"wiredTiger,omitempty"`
	OperationProfiling        *MongodSpecOperationProfiling `json:"operationProfiling,omitempty"`
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
