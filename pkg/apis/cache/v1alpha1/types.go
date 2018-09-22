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

type PerconaServerMongoDBSpecMongoDB struct {
	Port          int32  `json:"port,omitempty"`
	StorageEngine string `json:"storageEngine,omitempty"`
	ReplsetName   string `json:"replsetName,omitempty"`
}

type PerconaServerMongoDBSpec struct {
	Size    int32                            `json:"size"`
	Image   string                           `json:"image,omitempty"`
	MongoDB *PerconaServerMongoDBSpecMongoDB `json:"mongodb,omitempty"`
	RunGID  int64                            `json:"runGid,omitempty"`
	RunUID  int64                            `json:"runUid,omitempty"`
}
type PerconaServerMongoDBStatus struct {
	Nodes []string `json:"nodes"`
}
