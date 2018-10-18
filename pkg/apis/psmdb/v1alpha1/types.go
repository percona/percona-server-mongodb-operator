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

type PerconaServerMongoDBMongodMMAPv1 struct {
	NsSize     int  `json:"nsSize,omitempty"`
	Smallfiles bool `json:"smallfiles,omitempty"`
}

type PerconaServerMongoDBMongodWiredTiger struct {
	CacheSizeRatio float64 `json:"cacheSizeRatio,omitempty"`
}

type PerconaServerMongoDBMongodOperationProfiling struct {
	SlowMs int `json:"slowMs,omitempty"`
}

type PerconaServerMongoDBReplset struct {
	Name      string `json:"name,omitempty"`
	Size      int32  `json:"size,omitempty"`
	Configsvr bool   `json:"configsvr,omitempty"`
}

type PerconaServerMongoDBMongos struct {
	Size     int32 `json:"size,omitempty"`
	Cpus     int64 `json:"cpus,omitempty"`
	Memory   int64 `json:"memory,omitempty"`
	Storage  int64 `json:"storage,omitempty"`
	Port     int32 `json:"port,omitempty"`
	HostPort int32 `json:"hostPort,omitempty"`
}

type PerconaServerMongoDBSharding struct {
	Enabled   bool                         `json:"enabled,omitempty"`
	Configsvr *PerconaServerMongoDBReplset `json:"configsvr,omitempty"`
	Mongos    *PerconaServerMongoDBMongos  `json:"mongos,omitempty"`
}

type PerconaServerMongoDBMongod struct {
	Cpus               int64                                         `json:"cpus,omitempty"`
	Memory             int64                                         `json:"memory,omitempty"`
	Storage            int64                                         `json:"storage,omitempty"`
	Port               int32                                         `json:"port,omitempty"`
	HostPort           int32                                         `json:"hostPort,omitempty"`
	StorageEngine      string                                        `json:"storageEngine,omitempty"`
	Sharding           *PerconaServerMongoDBSharding                 `json:"sharding,omitempty"`
	Replsets           []*PerconaServerMongoDBReplset                `json:"replsets,omitempty"`
	MMAPv1             *PerconaServerMongoDBMongodMMAPv1             `json:"mmapv1,omitempty"`
	WiredTiger         *PerconaServerMongoDBMongodWiredTiger         `json:"wiredTiger,omitempty"`
	OperationProfiling *PerconaServerMongoDBMongodOperationProfiling `json:"operationProfiling,omitempty"`
}

type PerconaServerMongoDBSpec struct {
	Image    string                         `json:"image,omitempty"`
	RunUID   int64                          `json:"runUid,omitempty"`
	Mongod   *PerconaServerMongoDBMongod    `json:"mongod,omitempty"`
	Sharding *PerconaServerMongoDBSharding  `json:"sharding,omitempty"`
	Replsets []*PerconaServerMongoDBReplset `json:"replsets,omitempty"`
}

type PerconaServerMongoDBStatusReplset struct {
	Name        string `json:"name,omitempty"`
	Initialised bool   `json:"initialised,omitempty"`
	Uri         string `json:"uri,omitempty"`
}

type PerconaServerMongoDBStatus struct {
	Replsets []*PerconaServerMongoDBStatusReplset `json:"replsets"`
	Nodes    []string                             `json:"nodes"`
}
