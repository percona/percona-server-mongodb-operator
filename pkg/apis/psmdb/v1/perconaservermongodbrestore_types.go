package v1

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PerconaServerMongoDBRestoreSpec defines the desired state of PerconaServerMongoDBRestore
type PerconaServerMongoDBRestoreSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	ClusterName  string                            `json:"clusterName,omitempty"`
	Replset      string                            `json:"replset,omitempty"`
	BackupName   string                            `json:"backupName,omitempty"`
	BackupSource *PerconaServerMongoDBBackupStatus `json:"backupSource,omitempty"`
	StorageName  string                            `json:"storageName,omitempty"`
	PITR         *PITRestoreSpec                   `json:"pitr,omitempty"`
}

// RestoreState is for restore status states
type RestoreState string

const (
	RestoreStateNew       RestoreState = ""
	RestoreStateWaiting   RestoreState = "waiting"
	RestoreStateRequested RestoreState = "requested"
	RestoreStateRejected  RestoreState = "rejected"
	RestoreStateRunning   RestoreState = "running"
	RestoreStateError     RestoreState = "error"
	RestoreStateReady     RestoreState = "ready"
)

// PerconaServerMongoDBRestoreStatus defines the observed state of PerconaServerMongoDBRestore
type PerconaServerMongoDBRestoreStatus struct {
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	State          RestoreState `json:"state,omitempty"`
	PBMname        string       `json:"pbmName,omitempty"`
	Error          string       `json:"error,omitempty"`
	CompletedAt    *metav1.Time `json:"completed,omitempty"`
	LastTransition *metav1.Time `json:"lastTransition,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerconaServerMongoDBRestore is the Schema for the perconaservermongodbrestores API
// +k8s:openapi-gen=true
type PerconaServerMongoDBRestore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PerconaServerMongoDBRestoreSpec   `json:"spec,omitempty"`
	Status PerconaServerMongoDBRestoreStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerconaServerMongoDBRestoreList contains a list of PerconaServerMongoDBRestore
type PerconaServerMongoDBRestoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PerconaServerMongoDBRestore `json:"items"`
}

func (r *PerconaServerMongoDBRestore) CheckFields() error {
	if len(r.Spec.ClusterName) == 0 {
		return fmt.Errorf("spec clusterName field is empty")
	}

	if len(r.Spec.BackupName) == 0 && r.Spec.BackupSource == nil {
		return errors.New("one of backupName or backupSource is required")
	}

	if r.Spec.BackupSource != nil {
		if len(r.Spec.BackupSource.Destination) == 0 {
			return errors.New("backupSource destination is required")
		}

		if r.Spec.BackupSource.S3 != nil && !strings.HasPrefix(r.Spec.BackupSource.Destination, "s3://") {
			return errors.New("backupSource destination should use s3 protocol format")
		}

		if len(r.Spec.StorageName) == 0 && r.Spec.BackupSource.S3 == nil && r.Spec.BackupSource.Azure == nil {
			return errors.New("one of storageName, backupSource.s3 or backupSource.azure is required")
		}
	}

	if r.Spec.PITR != nil {
		switch r.Spec.PITR.Type {
		case PITRestoreTypeDate:
			if r.Spec.PITR.Date == nil {
				return errors.New("date is required for pitr restore by date")
			}

		case PITRestoreTypeLatest:
		// no additional fields required - no validation

		default:
			return errors.Errorf("undefined pitr restore type: %s", r.Spec.PITR.Type)
		}
	}

	return nil
}

type PITRestoreSpec struct {
	Type PITRestoreType  `json:"type,omitempty"`
	Date *PITRestoreDate `json:"date,omitempty"`
}

type PITRestoreType string

var (
	PITRestoreTypeDate   PITRestoreType = "date"
	PITRestoreTypeLatest PITRestoreType = "latest"
)

type PITRestoreDate struct {
	metav1.Time
}

func (t *PITRestoreDate) UnmarshalJSON(b []byte) (err error) {
	if len(b) == 4 && string(b) == "null" {
		t.Time = metav1.NewTime(time.Time{})
		return nil
	}

	var str string

	if err = json.Unmarshal(b, &str); err != nil {
		return err
	}

	pt, err := time.Parse("2006-01-02 15:04:05", str)
	if err != nil {
		return
	}

	t.Time = metav1.NewTime(pt.Local())
	return nil
}
