package pbm

import "strings"

func IsNotConfigured(err error) bool {
	messages := []string{
		"mongo: no documents in result",
		"storage undefined",
		"missed config",
	}

	for _, message := range messages {
		if strings.Contains(err.Error(), message) {
			return true
		}
	}

	return false
}

func IsAnotherOperationInProgress(err error) bool {
	return strings.Contains(err.Error(), "another operation in progress")
}
