package pbm

import "strings"

func IsNotConfigured(err error) bool {
	return strings.Contains(err.Error(), "mongo: no documents in result") || strings.Contains(err.Error(), "storage undefined")
}

func IsAnotherOperationInProgress(err error) bool {
	return strings.Contains(err.Error(), "another operation in progress")
}
