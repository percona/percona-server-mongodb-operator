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

type PerconaServerMongoDBSpecMongoDB struct {
	Port          int32                                      `json:"port,omitempty"`
	StorageEngine string                                     `json:"storageEngine,omitempty"`
	ReplsetName   string                                     `json:"replsetName,omitempty"`
	MMAPv1        *PerconaServerMongoDBSpecMongoDBMMAPv1     `json:"mmapv1,omitempty"`
	WiredTiger    *PerconaServerMongoDBSpecMongoDBWiredTiger `json:"wiredTiger,omitempty"`
}

type PerconaServerMongoDBSpec struct {
	Size    int32                            `json:"size"`
	Image   string                           `json:"image,omitempty"`
	RunGID  int64                            `json:"runGid,omitempty"`
	RunUID  int64                            `json:"runUid,omitempty"`
	MongoDB *PerconaServerMongoDBSpecMongoDB `json:"mongodb,omitempty"`
}
type PerconaServerMongoDBStatus struct {
	Nodes []string `json:"nodes"`
}
