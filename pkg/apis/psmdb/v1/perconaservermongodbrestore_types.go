package v1

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
)

// PerconaServerMongoDBRestoreSpec defines the desired state of PerconaServerMongoDBRestore
type PerconaServerMongoDBRestoreSpec struct {
	ClusterName  string                            `json:"clusterName,omitempty"`
	Replset      string                            `json:"replset,omitempty"`
	BackupName   string                            `json:"backupName,omitempty"`
	BackupSource *PerconaServerMongoDBBackupStatus `json:"backupSource,omitempty"`
	StorageName  string                            `json:"storageName,omitempty"`
	PITR         *PITRestoreSpec                   `json:"pitr,omitempty"`
	Selective    *SelectiveRestoreOpts             `json:"selective,omitempty"`
}

type SelectiveRestoreOpts struct {
	WithUsersAndRoles bool     `json:"withUsersAndRoles,omitempty"`
	Namespaces        []string `json:"namespaces,omitempty"`
}

func (s *SelectiveRestoreOpts) GetNamespaces() []string {
	if s == nil {
		return nil
	}
	return s.Namespaces
}

func (s *SelectiveRestoreOpts) GetWithUsersAndRoles() bool {
	if s == nil {
		return false
	}
	return s.WithUsersAndRoles
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
	State          RestoreState `json:"state,omitempty"`
	PBMname        string       `json:"pbmName,omitempty"`
	PITRTarget     string       `json:"pitrTarget,omitempty"`
	Error          string       `json:"error,omitempty"`
	CompletedAt    *metav1.Time `json:"completed,omitempty"`
	LastTransition *metav1.Time `json:"lastTransition,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// PerconaServerMongoDBRestore is the Schema for the perconaservermongodbrestores API
// +k8s:openapi-gen=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName="psmdb-restore"
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=".spec.clusterName",description="Cluster name"
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=".status.state",description="Job status"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp",description="Created time"
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

func (r *PerconaServerMongoDBRestore) SetDefaults() error {
	if bs := r.Spec.BackupSource; bs != nil {
		if bs.Type == "" {
			bs.Type = defs.LogicalBackup
		}
	}
	return nil
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

		if len(r.Spec.StorageName) == 0 && r.Spec.BackupSource.S3 == nil && r.Spec.BackupSource.Azure == nil && r.Spec.BackupSource.Filesystem == nil {
			return errors.New("one of storageName, backupSource.s3, backupSource.azure or backupSource.filesystem is required")
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

// +kubebuilder:validation:Type=string
type PITRestoreDate struct {
	metav1.Time `json:",inline"`
}

func (PITRestoreDate) OpenAPISchemaType() []string { return []string{"string"} }

func (PITRestoreDate) OpenAPISchemaFormat() string { return "" }

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

func (t *PITRestoreDate) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.Time.Format("2006-01-02 15:04:05"))
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

const (
	AnnotationRestoreInProgress = "percona.com/restore-in-progress"
	// AnnotationUpdateMongosFirst is an annotation used to force next smart update to be applied to mongos before mongod.
	AnnotationUpdateMongosFirst = "percona.com/update-mongos-first"
)
