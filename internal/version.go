package internal

import (
	"encoding/json"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/operator-framework/operator-sdk/pkg/k8sclient"
	kapierrors "k8s.io/apimachinery/pkg/api/errors"
)

// GetPlatform returns the Kubernetes platform type, first using the Spec 'platform'
// field or the serverVersion.Platform field if the Spec 'platform' field is not set
func GetPlatform(m *v1alpha1.PerconaServerMongoDB, serverVersion *v1alpha1.ServerVersion) v1alpha1.Platform {
	if m.Spec.Platform != nil {
		return *m.Spec.Platform
	}
	if serverVersion != nil {
		return serverVersion.Platform
	}
	return v1alpha1.PlatformKubernetes
}

// getServerVersion returns server version and platform (k8s|oc)
// stolen from: https://github.com/openshift/origin/blob/release-3.11/pkg/oc/cli/version/version.go#L106
func GetServerVersion() (*v1alpha1.ServerVersion, error) {
	version := &v1alpha1.ServerVersion{}

	client := k8sclient.GetKubeClient().Discovery().RESTClient()

	kubeVersionBody, err := client.Get().AbsPath("/version").Do().Raw()
	switch {
	case err == nil:
		err = json.Unmarshal(kubeVersionBody, &version.Info)
		if err != nil && len(kubeVersionBody) > 0 {
			return nil, err
		}
		version.Platform = v1alpha1.PlatformKubernetes
	case kapierrors.IsNotFound(err) || kapierrors.IsUnauthorized(err) || kapierrors.IsForbidden(err):
		// this is fine! just try to get /version/openshift
	default:
		return nil, err
	}

	ocVersionBody, err := client.Get().AbsPath("/version/openshift").Do().Raw()
	switch {
	case err == nil:
		err = json.Unmarshal(ocVersionBody, &version.Info)
		if err != nil && len(ocVersionBody) > 0 {
			return nil, err
		}
		version.Platform = v1alpha1.PlatformOpenshift
	case kapierrors.IsNotFound(err) || kapierrors.IsUnauthorized(err) || kapierrors.IsForbidden(err):
	default:
		return nil, err
	}

	return version, nil
}
