package v1

import (
	"fmt"

	"github.com/percona/percona-backup-mongodb/pbm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PerconaServerMongoDBBackupSpec defines the desired state of PerconaServerMongoDBBackup
type PerconaServerMongoDBBackupSpec struct {
	PSMDBCluster     string              `json:"psmdbCluster,omitempty"` // TODO: Remove after v1.15
	ClusterName      string              `json:"clusterName,omitempty"`
	StorageName      string              `json:"storageName,omitempty"`
	Compression      pbm.CompressionType `json:"compressionType,omitempty"`
	CompressionLevel *int                `json:"compressionLevel,omitempty"`
}

type BackupState string

const (
	BackupStateNew       BackupState = ""
	BackupStateWaiting   BackupState = "waiting"
	BackupStateRequested BackupState = "requested"
	BackupStateRejected  BackupState = "rejected"
	BackupStateRunning   BackupState = "running"
	BackupStateError     BackupState = "error"
	BackupStateReady     BackupState = "ready"
)

// PerconaServerMongoDBBackupStatus defines the observed state of PerconaServerMongoDBBackup
type PerconaServerMongoDBBackupStatus struct {
	State          BackupState             `json:"state,omitempty"`
	StartAt        *metav1.Time            `json:"start,omitempty"`
	CompletedAt    *metav1.Time            `json:"completed,omitempty"`
	LastTransition *metav1.Time            `json:"lastTransition,omitempty"`
	Destination    string                  `json:"destination,omitempty"`
	StorageName    string                  `json:"storageName,omitempty"`
	S3             *BackupStorageS3Spec    `json:"s3,omitempty"`
	Azure          *BackupStorageAzureSpec `json:"azure,omitempty"`
	PBMname        string                  `json:"pbmName,omitempty"`
	Error          string                  `json:"error,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerconaServerMongoDBBackup is the Schema for the perconaservermongodbbackups API
//+k8s:openapi-gen=true
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName="psmdb-backup"
//+kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=".spec.clusterName",description="Cluster name"
//+kubebuilder:printcolumn:name="Storage",type=string,JSONPath=".spec.storageName",description="Storage name"
//+kubebuilder:printcolumn:name="Destination",type=string,JSONPath=".status.destination",description="Backup destination"
//+kubebuilder:printcolumn:name="Status",type=string,JSONPath=".status.state",description="Job status"
//+kubebuilder:printcolumn:name="Completed",type=date,JSONPath=".status.completed",description="Completed time"
//+kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp",description="Created time"
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
		return fmt.Errorf("spec clusterName and deprecated psmdbCluster fields are empty")
	}
	if string(p.Spec.Compression) == "" {
		p.Spec.Compression = pbm.CompressionTypeGZIP
	}
	return nil
}

// GetClusterName returns ClusterName if it's not empty. Otherwise, it will return PSMDBCluster.
// TODO: Remove after v1.15
func (p *PerconaServerMongoDBBackupSpec) GetClusterName() string {
	if len(p.ClusterName) > 0 {
		return p.ClusterName
	}
	return p.PSMDBCluster
}
