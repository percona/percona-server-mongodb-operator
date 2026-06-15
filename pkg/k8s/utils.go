package k8s

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// GetOperatorNamespace returns the namespace of the operator pod
func GetOperatorNamespace() (string, error) {
	nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(nsBytes)), nil
}

func IsPodReady(pod corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning || pod.DeletionTimestamp != nil {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.ContainersReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}

func DeleteIfExists(ctx context.Context, c client.Client, obj client.Object) error {
	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); k8serrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return errors.Wrapf(err, "failed to get %T %s/%s", obj, obj.GetNamespace(), obj.GetName())
	}

	// Deletion was already requested
	if obj.GetDeletionTimestamp() != nil {
		return nil
	}

	if err := c.Delete(ctx, obj); client.IgnoreNotFound(err) != nil {
		return errors.Wrapf(err, "failed to delete %T %s/%s", obj, obj.GetNamespace(), obj.GetName())
	}
	return nil
}
