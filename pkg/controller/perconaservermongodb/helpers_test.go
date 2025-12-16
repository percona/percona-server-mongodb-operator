package perconaservermongodb

import (
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func updateObj[T client.Object](t *testing.T, obj T, f func(obj T)) T {
	t.Helper()

	f(obj)

	return obj
}
