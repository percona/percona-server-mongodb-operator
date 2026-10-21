package mcs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsAvailable(t *testing.T) {
	t.Parallel()

	available = true
	assert.True(t, IsAvailable())

	available = false
	assert.False(t, IsAvailable())
}

func TestServiceExport(t *testing.T) {
	t.Parallel()

	namespace := "test-namespace"
	name := "test-service"
	labels := map[string]string{
		"app": "test",
	}

	se := ServiceExport(namespace, name, labels)

	assert.Equal(t, name, se.Name)
	assert.Equal(t, namespace, se.Namespace)
	assert.Equal(t, "test", se.Labels["app"])
}

func TestServiceExportList(t *testing.T) {
	t.Parallel()

	seList := ServiceExportList()

	assert.Equal(t, "ServiceExportList", seList.Kind)
}
