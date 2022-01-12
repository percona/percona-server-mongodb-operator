package v1

import (
	"fmt"

	"github.com/percona/percona-backup-mongodb/pbm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PerconaServerMongoDBBackupSpec defines the desired state of PerconaServerMongoDBBackup
type PerconaServerMongoDBBackupSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	PSMDBCluster     string              `json:"psmdbCluster,omitempty"` // TODO: Remove after v1.15
	ClusterName      string              `json:"clusterName,omitempty"`
	StorageName      string              `json:"storageName,omitempty"`
	Compression      pbm.CompressionType `json:"compressionType,omitempty"`
	CompressionLevel *int64              `json:"compressionLevel,omitempty"`
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
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
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
// +k8s:openapi-gen=true
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
