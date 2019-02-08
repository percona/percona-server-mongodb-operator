package testutils

import (
	"errors"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	MockUnexpectedError    = errors.New("mock unexpected error")
	MockAlreadyExistsError = k8serrors.NewAlreadyExists(schema.GroupResource{
		Group:    "group",
		Resource: "resource",
	}, "mock")
)
