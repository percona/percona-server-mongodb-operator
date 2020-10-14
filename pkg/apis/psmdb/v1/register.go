// NOTE: Boilerplate only.  Ignore this file.

// Package v1 contains API Schema definitions for the psmdb v1 API group
// +k8s:deepcopy-gen=package,register
// +groupName=psmdb.percona.com
package v1

import (
	"strings"

	"github.com/percona/percona-server-mongodb-operator/version"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	mainSchemeGroupVersion = schema.GroupVersion{Group: "psmdb.percona.com", Version: strings.Replace("v"+version.Version, ".", "-", -1)}
	MainSchemeBuilder      = scheme.Builder{GroupVersion: mainSchemeGroupVersion}
	// SchemeGroupVersion is group version used to register these objects
	SchemeGroupVersion = schema.GroupVersion{Group: "psmdb.percona.com", Version: "v1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: SchemeGroupVersion}
)

func init() {
	MainSchemeBuilder.Register(&PerconaServerMongoDB{}, &PerconaServerMongoDBList{})
	SchemeBuilder.Register(&PerconaServerMongoDBBackup{}, &PerconaServerMongoDBBackupList{}, &PerconaServerMongoDBRestore{}, &PerconaServerMongoDBRestoreList{})
}
