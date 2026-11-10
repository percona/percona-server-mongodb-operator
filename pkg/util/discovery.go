package util

import (
	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersionKindExists checks to see whether a given Kind for a given
// GroupVersion exists in the Kubernetes API Server.
func GroupVersionKindExists(cl client.Client, gvk schema.GroupVersionKind) (bool, error) {
	if gvk.Group == "" || gvk.Version == "" || gvk.Kind == "" {
		panic("one of group, version or kind is unspecified")
	}

	if _, err := cl.RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version); meta.IsNoMatchError(err) {
		return false, nil
	} else if err != nil {
		return false, errors.Wrap(err, "failed to get REST mapping")
	}

	return true, nil
}
