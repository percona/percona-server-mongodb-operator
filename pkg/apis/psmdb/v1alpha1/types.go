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

type PerconaServerMongoDBSpecMongoDBMMAPv1 struct {
	NsSize     int  `json:"nsSize,omitempty"`
	Smallfiles bool `json:"smallfiles,omitempty"`
}

type PerconaServerMongoDBSpecMongoDBWiredTiger struct {
	CacheSizeRatio float64 `json:"cacheSizeRatio,omitempty"`
}

type PerconaServerMongoDBSpecMongoDBOperationProfiling struct {
	SlowMs int `json:"slowMs,omitempty"`
}

type PerconaServerMongoDBReplset struct {
	Name      string `json:"name,omitempty"`
	Size      int32  `json:"size,omitempty"`
	Configsvr bool   `json:"configsvr,omitempty"`
}

type PerconaServerMongoDBSpecMongoDB struct {
	Cpus               int64                                              `json:"cpus,omitempty"`
	Memory             int64                                              `json:"memory,omitempty"`
	Storage            int64                                              `json:"storage,omitempty"`
	Port               int32                                              `json:"port,omitempty"`
	HostPort           int32                                              `json:"hostPort,omitempty"`
	StorageEngine      string                                             `json:"storageEngine,omitempty"`
	Sharding           bool                                               `json:"sharding,omitempty"`
	Replsets           []*PerconaServerMongoDBReplset                     `json:"replsets,omitempty"`
	MMAPv1             *PerconaServerMongoDBSpecMongoDBMMAPv1             `json:"mmapv1,omitempty"`
	WiredTiger         *PerconaServerMongoDBSpecMongoDBWiredTiger         `json:"wiredTiger,omitempty"`
	OperationProfiling *PerconaServerMongoDBSpecMongoDBOperationProfiling `json:"operationProfiling,omitempty"`
}

type PerconaServerMongoDBSpec struct {
	Image   string                           `json:"image,omitempty"`
	RunUID  int64                            `json:"runUid,omitempty"`
	MongoDB *PerconaServerMongoDBSpecMongoDB `json:"mongodb,omitempty"`
}

type PerconaServerMongoDBStatus struct {
	Initialised bool     `json:"initialised"`
	Nodes       []string `json:"nodes"`
	Uri         string   `json:"uri"`
}
