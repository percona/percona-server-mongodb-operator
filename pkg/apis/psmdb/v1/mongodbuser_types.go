package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MongoDBUser is the Schema for the mongodbusers API
// +k8s:openapi-gen=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName="mdbuser"
// +kubebuilder:printcolumn:name="Username",type="string",JSONPath=".spec.username"
// +kubebuilder:printcolumn:name="Database",type="string",JSONPath=".spec.database"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type MongoDBUser struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MongoDBUserSpec   `json:"spec,omitempty"`
	Status MongoDBUserStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MongoDBUserList contains a list of MongoDBUser
type MongoDBUserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MongoDBUser `json:"items"`
}

// MongoDBUserSpec defines the desired state of MongoDBUser
type MongoDBUserSpec struct {
	// ClusterRef specifies the MongoDB cluster this user belongs to
	ClusterRef ClusterReference `json:"clusterRef"`

	// Username is the MongoDB username
	Username string `json:"username"`

	// Database is the database where the user will be created (default: admin)
	Database string `json:"database,omitempty"`

	// PasswordSecretRef references the secret containing the user password
	PasswordSecretRef *SecretKeySelector `json:"passwordSecretRef,omitempty"`

	// Roles defines the global roles assigned to this user (cluster-wide permissions)
	Roles []UserRole `json:"roles,omitempty"`

	// DatabaseAccess defines user access to specific databases
	DatabaseAccess []UserDatabaseAccess `json:"databaseAccess,omitempty"`

	// AuthenticationRestrictions defines authentication restrictions for this user
	AuthenticationRestrictions []RoleAuthenticationRestriction `json:"authenticationRestrictions,omitempty"`

	// IsExternal specifies if this is an external authentication user ($external database)
	IsExternal bool `json:"isExternal,omitempty"`
}

// ClusterReference specifies a reference to a MongoDB cluster
type ClusterReference struct {
	// Name is the name of the MongoDB cluster
	Name string `json:"name"`

	// Namespace is the namespace of the MongoDB cluster (optional, defaults to same namespace)
	Namespace string `json:"namespace,omitempty"`
}

// MongoDBUserStatus defines the observed state of MongoDBUser
type MongoDBUserStatus struct {
	// State represents the current state of the user
	State AppState `json:"state,omitempty"`

	// Message provides additional information about the current state
	Message string `json:"message,omitempty"`

	// Conditions represents the latest available observations of the user's current state
	Conditions []UserCondition `json:"conditions,omitempty"`

	// ObservedGeneration represents the .metadata.generation that the condition was set based upon
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSyncTime represents the last time the user was successfully synchronized with MongoDB
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
}

// UserCondition represents a condition of a MongoDBUser
type UserCondition struct {
	// Type of user condition
	Type UserConditionType `json:"type"`

	// Status of the condition, one of True, False, Unknown
	Status ConditionStatus `json:"status"`

	// Last time the condition transitioned from one status to another
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`

	// The reason for the condition's last transition
	Reason string `json:"reason,omitempty"`

	// A human readable message indicating details about the transition
	Message string `json:"message,omitempty"`
}

// UserDatabaseAccess defines user access to a specific database
type UserDatabaseAccess struct {
	// DatabaseName is the name of the database
	DatabaseName string `json:"databaseName"`

	// Roles defines the roles assigned to this user for this specific database
	Roles []string `json:"roles"`

	// Collections defines specific collections this user can access (optional)
	// If empty, applies to all collections in the database
	Collections []string `json:"collections,omitempty"`

	// ReadOnly specifies if this access is read-only
	ReadOnly bool `json:"readOnly,omitempty"`

	// ExpiresAt specifies when this access should expire (optional)
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
}

// UserConditionType represents a condition type for MongoDBUser
type UserConditionType string

const (
	// UserConditionReady indicates that the user is ready
	UserConditionReady UserConditionType = "Ready"

	// UserConditionCreated indicates that the user has been created in MongoDB
	UserConditionCreated UserConditionType = "Created"

	// UserConditionUpdated indicates that the user has been updated in MongoDB
	UserConditionUpdated UserConditionType = "Updated"

	// UserConditionFailed indicates that the user operation has failed
	UserConditionFailed UserConditionType = "Failed"
)
