package v1

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

// PerconaServerMongoDBBackupSpec defines the desired state of PerconaServerMongoDBBackup
type PerconaServerMongoDBBackupSpec struct {
	ClusterName      string                   `json:"clusterName,omitempty"`
	StorageName      string                   `json:"storageName,omitempty"`
	Compression      compress.CompressionType `json:"compressionType,omitempty"`
	CompressionLevel *int                     `json:"compressionLevel,omitempty"`

	// +kubebuilder:validation:Enum={logical,physical,incremental,incremental-base}
	Type                    defs.BackupType `json:"type,omitempty"`
	StartingDeadlineSeconds *int64          `json:"startingDeadlineSeconds,omitempty"`
}

type BackupState string

const (
	BackupStateNew       BackupState = ""
	BackupStateWaiting   BackupState = "waiting"
	BackupStateRequested BackupState = "requested"
	BackupStateRunning   BackupState = "running"
	BackupStateError     BackupState = "error"
	BackupStateReady     BackupState = "ready"
)

// PerconaServerMongoDBBackupStatus defines the observed state of PerconaServerMongoDBBackup
type PerconaServerMongoDBBackupStatus struct {
	Type         defs.BackupType              `json:"type,omitempty"`
	State        BackupState                  `json:"state,omitempty"`
	Destination  string                       `json:"destination,omitempty"`
	StorageName  string                       `json:"storageName,omitempty"`
	S3           *BackupStorageS3Spec         `json:"s3,omitempty"`
	Azure        *BackupStorageAzureSpec      `json:"azure,omitempty"`
	Filesystem   *BackupStorageFilesystemSpec `json:"filesystem,omitempty"`
	ReplsetNames []string                     `json:"replsetNames,omitempty"`
	PBMname      string                       `json:"pbmName,omitempty"`
	Size         string                       `json:"size,omitempty"`

	// Deprecated: Use PBMPods instead
	PBMPod  string            `json:"pbmPod,omitempty"`
	PBMPods map[string]string `json:"pbmPods,omitempty"`
	Error   string            `json:"error,omitempty"`

	StartAt              *metav1.Time `json:"start,omitempty"`
	CompletedAt          *metav1.Time `json:"completed,omitempty"`
	LastWriteAt          *metav1.Time `json:"lastWriteAt,omitempty"`
	LastTransition       *metav1.Time `json:"lastTransition,omitempty"`
	LatestRestorableTime *metav1.Time `json:"latestRestorableTime,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerconaServerMongoDBBackup is the Schema for the perconaservermongodbbackups API
// +k8s:openapi-gen=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName="psmdb-backup"
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=".spec.clusterName",description="Cluster name"
// +kubebuilder:printcolumn:name="Storage",type=string,JSONPath=".spec.storageName",description="Storage name"
// +kubebuilder:printcolumn:name="Destination",type=string,JSONPath=".status.destination",description="Backup destination"
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".status.type",description="Backup type"
// +kubebuilder:printcolumn:name="Size",type=string,JSONPath=".status.size",description="Backup size"
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=".status.state",description="Job status"
// +kubebuilder:printcolumn:name="Completed",type=date,JSONPath=".status.completed",description="Completed time"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp",description="Created time"
type PerconaServerMongoDBBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PerconaServerMongoDBBackupSpec   `json:"spec,omitempty"`
	Status PerconaServerMongoDBBackupStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerconaServerMongoDBBackupList contains a list of PerconaServerMongoDBBackup
type PerconaServerMongoDBBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PerconaServerMongoDBBackup `json:"items"`
}

func (p *PerconaServerMongoDBBackup) CheckFields() error {
	if len(p.Spec.StorageName) == 0 {
		return fmt.Errorf("spec storageName field is empty")
	}
	if len(p.Spec.GetClusterName()) == 0 {
		return fmt.Errorf("spec clusterName is empty")
	}
	if string(p.Spec.Type) == "" {
		p.Spec.Type = defs.LogicalBackup
	}
	if string(p.Spec.Compression) == "" {
		p.Spec.Compression = compress.CompressionTypeGZIP
	}
	return nil
}

const (
	BackupTypeIncrementalBase defs.BackupType = defs.IncrementalBackup + "-base"
)

func (p *PerconaServerMongoDBBackup) PBMBackupType() defs.BackupType {
	if p.Spec.Type == BackupTypeIncrementalBase {
		return defs.IncrementalBackup
	}
	return p.Spec.Type
}

func (p *PerconaServerMongoDBBackup) IsBackupTypeIncrementalBase() bool {
	return p.Spec.Type == BackupTypeIncrementalBase
}

// GetClusterName returns ClusterName.
func (p *PerconaServerMongoDBBackupSpec) GetClusterName() string {
	return p.ClusterName
}
