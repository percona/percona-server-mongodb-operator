package mcs

import (
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"strings"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	mcs "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

var (
	schemeBuilder = &runtime.SchemeBuilder{}

	// AddToScheme is used to register MCS CRDs to a runtime.Scheme
	AddToScheme = schemeBuilder.AddToScheme

	// MCSSchemeGroupVersion is group version used to register Kubernetes Multi-Cluster Services (MCS) objects
	MCSSchemeGroupVersion = schema.GroupVersion{}
)

func addKnownTypes(scheme *runtime.Scheme) error {
	// Register Kubernetes Multi-Cluster Services (MCS) objects.
	scheme.AddKnownTypes(MCSSchemeGroupVersion,
		&mcs.ServiceExport{},
		&mcs.ServiceExportList{},
		&mcs.ServiceImport{},
		&mcs.ServiceImportList{})
	metav1.AddToGroupVersion(scheme, MCSSchemeGroupVersion)

	return nil
}

func Register(dc *discovery.DiscoveryClient) error {
	_, resources, err := dc.ServerGroupsAndResources()
	if err != nil {
		return errors.Wrap(err, "get api groups and resources")
	}

outer:
	for _, r := range resources {
		for _, resource := range r.APIResources {
			if resource.Kind == "ServiceExport" {
				gv := strings.Split(r.GroupVersion, "/")

				MCSSchemeGroupVersion.Group = gv[0]
				MCSSchemeGroupVersion.Version = gv[1]

				break outer
			}
		}
	}

	if MCSSchemeGroupVersion.Group == "" {
		return errors.New("Kind ServiceExport is not found in any of the API groups")
	}

	schemeBuilder.Register(addKnownTypes)

	return nil
}

// ServiceExport returns a ServiceExport object needed for Multi-cluster Services
func ServiceExport(m *api.PerconaServerMongoDB, name string, ls map[string]string) *mcs.ServiceExport {
	return &mcs.ServiceExport{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceExport",
			APIVersion: MCSSchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: m.Namespace,
			Labels:    ls,
		},
	}
}

// ServiceExportList returns a ServiceExport list needed for Multi-cluster Services
func ServiceExportList() *mcs.ServiceExportList {
	return &mcs.ServiceExportList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceExportList",
			APIVersion: MCSSchemeGroupVersion.String(),
		},
	}
}
