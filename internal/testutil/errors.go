package testutil

import (
	"errors"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	UnexpectedError    = errors.New("mock unexpected error")
	AlreadyExistsError = k8serrors.NewAlreadyExists(schema.GroupResource{
		Group:    "alreadyExists",
		Resource: "alreadyExists",
	}, "mock")
)
