package k8s

import (
	"fmt"
	"os"

	"github.com/pkg/errors"
	"k8s.io/client-go/discovery"
)

const WatchNamespaceEnvVar = "WATCH_NAMESPACE"

// GetWatchNamespace returns the namespace the operator should be watching for changes
func GetWatchNamespace() (string, error) {
	ns, found := os.LookupEnv(WatchNamespaceEnvVar)
	if !found {
		return "", fmt.Errorf("%s must be set", WatchNamespaceEnvVar)
	}
	return ns, nil
}

// APIGroupExists returns true if given API group exists in the cluster
func APIGroupExists(dc discovery.DiscoveryInterface, name string) (bool, error) {
	apiGroups, _, err := dc.ServerGroupsAndResources()
	if err != nil {
		return false, errors.Wrap(err, "get server groups and resources")
	}

	for _, apiGroup := range apiGroups {
		if apiGroup.Name == name {
			return true, nil
		}
	}

	return false, nil
}
