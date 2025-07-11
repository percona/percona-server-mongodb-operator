package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MongoDBDatabase is the Schema for the mongodbdatabases API
// +k8s:openapi-gen=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName="mdbdb"
// +kubebuilder:printcolumn:name="Database",type="string",JSONPath=".spec.name"
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".spec.clusterRef.name"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type MongoDBDatabase struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MongoDBDatabaseSpec   `json:"spec,omitempty"`
	Status MongoDBDatabaseStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MongoDBDatabaseList contains a list of MongoDBDatabase
type MongoDBDatabaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MongoDBDatabase `json:"items"`
}

// MongoDBDatabaseSpec defines the desired state of MongoDBDatabase
type MongoDBDatabaseSpec struct {
	// ClusterRef specifies the MongoDB cluster this database belongs to
	ClusterRef ClusterReference `json:"clusterRef"`

	// Name is the name of the database
	Name string `json:"name"`

	// Collections defines the collections to be created in this database
	Collections []DatabaseCollection `json:"collections,omitempty"`

	// Note: User access is managed separately via MongoDBDatabaseAccess CR

	// Options defines additional database options
	Options *DatabaseOptions `json:"options,omitempty"`
}

// DatabaseCollection defines a collection in the database
type DatabaseCollection struct {
	// Name is the name of the collection
	Name string `json:"name"`

	// Capped specifies if this is a capped collection
	Capped bool `json:"capped,omitempty"`

	// Size specifies the size limit for a capped collection (in bytes)
	Size int64 `json:"size,omitempty"`

	// Max specifies the maximum number of documents for a capped collection
	Max int64 `json:"max,omitempty"`

	// Validator defines the validation rules for the collection
	Validator map[string]string `json:"validator,omitempty"`

	// Indexes defines the indexes to be created on this collection
	Indexes []CollectionIndex `json:"indexes,omitempty"`
}

// CollectionIndex defines an index on a collection
type CollectionIndex struct {
	// Name is the name of the index
	Name string `json:"name"`

	// Keys defines the index keys
	Keys map[string]int `json:"keys"`

	// Unique specifies if this is a unique index
	Unique bool `json:"unique,omitempty"`

	// Sparse specifies if this is a sparse index
	Sparse bool `json:"sparse,omitempty"`

	// Background specifies if this index should be built in the background
	Background bool `json:"background,omitempty"`

	// PartialFilterExpression defines a partial filter expression for the index
	PartialFilterExpression map[string]string `json:"partialFilterExpression,omitempty"`
}

// Note: DatabaseUserAccess has been moved to MongoDBDatabaseAccess CR

// DatabaseOptions defines additional options for the database
type DatabaseOptions struct {
	// ReadOnly specifies if the database is read-only
	ReadOnly bool `json:"readOnly,omitempty"`

	// ShardingEnabled specifies if sharding is enabled for this database
	ShardingEnabled bool `json:"shardingEnabled,omitempty"`

	// PrimaryShard specifies the primary shard for this database (for sharded clusters)
	PrimaryShard string `json:"primaryShard,omitempty"`
}

// MongoDBDatabaseStatus defines the observed state of MongoDBDatabase
type MongoDBDatabaseStatus struct {
	// State represents the current state of the database
	State AppState `json:"state,omitempty"`

	// Message provides additional information about the current state
	Message string `json:"message,omitempty"`

	// Conditions represents the latest available observations of the database's current state
	Conditions []DatabaseCondition `json:"conditions,omitempty"`

	// ObservedGeneration represents the .metadata.generation that the condition was set based upon
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSyncTime represents the last time the database was successfully synchronized with MongoDB
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// CollectionsStatus provides status information about the collections
	CollectionsStatus []CollectionStatus `json:"collectionsStatus,omitempty"`

	// Note: User status is managed separately via MongoDBDatabaseAccess CR
}

// DatabaseCondition represents a condition of a MongoDBDatabase
type DatabaseCondition struct {
	// Type of database condition
	Type DatabaseConditionType `json:"type"`

	// Status of the condition, one of True, False, Unknown
	Status ConditionStatus `json:"status"`

	// Last time the condition transitioned from one status to another
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`

	// The reason for the condition's last transition
	Reason string `json:"reason,omitempty"`

	// A human readable message indicating details about the transition
	Message string `json:"message,omitempty"`
}

// DatabaseConditionType represents a condition type for MongoDBDatabase
type DatabaseConditionType string

const (
	// DatabaseConditionReady indicates that the database is ready
	DatabaseConditionReady DatabaseConditionType = "Ready"

	// DatabaseConditionCreated indicates that the database has been created in MongoDB
	DatabaseConditionCreated DatabaseConditionType = "Created"

	// DatabaseConditionUpdated indicates that the database has been updated in MongoDB
	DatabaseConditionUpdated DatabaseConditionType = "Updated"

	// DatabaseConditionFailed indicates that the database operation has failed
	DatabaseConditionFailed DatabaseConditionType = "Failed"
)

// CollectionStatus provides status information about a collection
type CollectionStatus struct {
	// Name is the name of the collection
	Name string `json:"name"`

	// State represents the current state of the collection
	State AppState `json:"state,omitempty"`

	// Message provides additional information about the collection state
	Message string `json:"message,omitempty"`

	// IndexesStatus provides status information about the collection indexes
	IndexesStatus []IndexStatus `json:"indexesStatus,omitempty"`
}

// IndexStatus provides status information about an index
type IndexStatus struct {
	// Name is the name of the index
	Name string `json:"name"`

	// State represents the current state of the index
	State AppState `json:"state,omitempty"`

	// Message provides additional information about the index state
	Message string `json:"message,omitempty"`
}

// Note: DatabaseUserStatus has been moved to MongoDBDatabaseAccess CR 