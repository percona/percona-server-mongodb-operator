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

type PerconaServerMongoDBSpecMongoDB struct {
	Port               int32                                              `json:"port,omitempty"`
	StorageEngine      string                                             `json:"storageEngine,omitempty"`
	ReplsetName        string                                             `json:"replsetName,omitempty"`
	MMAPv1             *PerconaServerMongoDBSpecMongoDBMMAPv1             `json:"mmapv1,omitempty"`
	WiredTiger         *PerconaServerMongoDBSpecMongoDBWiredTiger         `json:"wiredTiger,omitempty"`
	OperationProfiling *PerconaServerMongoDBSpecMongoDBOperationProfiling `json:"operationProfiling,omitempty"`
}

type PerconaServerMongoDBSpecCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
	Source   string `json:"source,omitempty"`
}

type PerconaServerMongoDBSpec struct {
	Size           int32                                `json:"size"`
	Image          string                               `json:"image,omitempty"`
	RunGID         int64                                `json:"runGid,omitempty"`
	RunUID         int64                                `json:"runUid,omitempty"`
	UserAdmin      *PerconaServerMongoDBSpecCredential  `json:"userAdmin,omitempty"`
	ClusterAdmin   *PerconaServerMongoDBSpecCredential  `json:"clusterAdmin,omitempty"`
	ClusterMonitor *PerconaServerMongoDBSpecCredential  `json:"clusterMonitor,omitempty"`
	AddUsers       []PerconaServerMongoDBSpecCredential `json:"addCredentials,omitempty"`
	MongoDB        *PerconaServerMongoDBSpecMongoDB     `json:"mongodb,omitempty"`
}

type PerconaServerMongoDBStatus struct {
	Initialised bool     `json:"initialised"`
	Nodes       []string `json:"nodes"`
	Uri         string   `json:"uri"`
}
