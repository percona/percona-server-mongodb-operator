package mcs

import (
	"testing"
)

func TestIsAvailable(t *testing.T) {
	// Test IsAvailable function
	available = true
	if !IsAvailable() {
		t.Error("Expected IsAvailable to return true when available is true")
	}

	available = false
	if IsAvailable() {
		t.Error("Expected IsAvailable to return false when available is false")
	}
}

func TestMCSSchemeGroupVersion(t *testing.T) {
	// Test that MCSSchemeGroupVersion is initialized correctly
	if MCSSchemeGroupVersion.Group != "" {
		t.Errorf("Expected MCSSchemeGroupVersion.Group to be empty initially, got: %s", MCSSchemeGroupVersion.Group)
	}
	if MCSSchemeGroupVersion.Version != "" {
		t.Errorf("Expected MCSSchemeGroupVersion.Version to be empty initially, got: %s", MCSSchemeGroupVersion.Version)
	}
}

func TestServiceExport(t *testing.T) {
	// Test ServiceExport creation
	namespace := "test-namespace"
	name := "test-service"
	labels := map[string]string{
		"app": "test",
	}

	se := ServiceExport(namespace, name, labels)

	if se.Name != name {
		t.Errorf("Expected ServiceExport name to be %s, got: %s", name, se.Name)
	}

	if se.Namespace != namespace {
		t.Errorf("Expected ServiceExport namespace to be %s, got: %s", namespace, se.Namespace)
	}

	if se.Labels["app"] != "test" {
		t.Errorf("Expected ServiceExport label 'app' to be 'test', got: %s", se.Labels["app"])
	}
}

func TestServiceExportList(t *testing.T) {
	// Test ServiceExportList creation
	seList := ServiceExportList()

	if seList.Kind != "ServiceExportList" {
		t.Errorf("Expected ServiceExportList Kind to be 'ServiceExportList', got: %s", seList.Kind)
	}
}
