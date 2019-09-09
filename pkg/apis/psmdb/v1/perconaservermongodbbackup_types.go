package v1

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PerconaServerMongoDBBackupSpec defines the desired state of PerconaServerMongoDBBackup
type PerconaServerMongoDBBackupSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	PSMDBCluster string `json:"psmdbCluster,omitempty"`
	StorageName  string `json:"storageName,omitempty"`
}

type PerconaSMDBStatusState string

const (
	StateRequested PerconaSMDBStatusState = "requested"
	StateRejected  PerconaSMDBStatusState = "rejected"
	StateReady     PerconaSMDBStatusState = "ready"
)

// PerconaServerMongoDBBackupStatus defines the observed state of PerconaServerMongoDBBackup
type PerconaServerMongoDBBackupStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	State         PerconaSMDBStatusState `json:"state,omitempty"`
	StartAt       *metav1.Time           `json:"start,omitempty"`
	CompletedAt   *metav1.Time           `json:"completed,omitempty"`
	LastScheduled *metav1.Time           `json:"lastscheduled,omitempty"`
	Destination   string                 `json:"destination,omitempty"`
	StorageName   string                 `json:"storageName,omitempty"`
	S3            *BackupStorageS3Spec   `json:"s3,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerconaServerMongoDBBackup is the Schema for the perconaservermongodbbackups API
// +k8s:openapi-gen=true
type PerconaServerMongoDBBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec              PerconaServerMongoDBBackupSpec   `json:"spec,omitempty"`
	Status            PerconaServerMongoDBBackupStatus `json:"status,omitempty"`
	SchedulerName     string                           `json:"schedulerName,omitempty"`
	Affinity          *PodAffinity                     `json:"affinity,omitempty"`
	Tolerations       []corev1.Toleration              `json:"tolerations,omitempty"`
	PriorityClassName string                           `json:"priorityClassName,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerconaServerMongoDBBackupList contains a list of PerconaServerMongoDBBackup
type PerconaServerMongoDBBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PerconaServerMongoDBBackup `json:"items"`
}

type PSMDBBackupState string

const (
	BackupStarting  PSMDBBackupState = "Starting"
	BackupFailed                     = "Failed"
	BackupSucceeded                  = "Ready"
)

func (p *PerconaServerMongoDBBackup) CheckFields() error {
	if len(p.Spec.StorageName) == 0 {
		return fmt.Errorf("spec storageName field is empty")
	}
	if len(p.Spec.PSMDBCluster) == 0 {
		return fmt.Errorf("spec psmsdbCluster field is empty")
	}
	return nil
}
