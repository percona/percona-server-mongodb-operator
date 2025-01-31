package errors

import (
	stderrors "errors"

	gerrs "github.com/pkg/errors"
)

// ErrNotFound - object not found
var ErrNotFound = New("not found")

func New(text string) error {
	return stderrors.New(text) //nolint:goerr113
}

func Errorf(format string, args ...any) error {
	return gerrs.Errorf(format, args...)
}

func Wrap(cause error, text string) error {
	return gerrs.WithMessage(cause, text)
}

func Wrapf(cause error, format string, args ...any) error {
	return gerrs.WithMessagef(cause, format, args...)
}

func Is(cause, target error) bool {
	return gerrs.Is(cause, target)
}

func As(cause error, target interface{}) bool {
	return gerrs.As(cause, target)
}

func Unwrap(cause error) error {
	return gerrs.Unwrap(cause)
}

func Cause(err error) error {
	return gerrs.Cause(err)
}

func Join(errs ...error) error {
	return stderrors.Join(errs...)
}
