package pbm

import (
	"strings"

	"github.com/pkg/errors"
)

func wrapExecError(err error, cmd []string) error {
	return errors.Wrapf(err, "run '%s'", strings.Join(cmd, " "))
}

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

func BackupNotFound(err error) bool {
	return strings.Contains(err.Error(), "get backup metadata: not found")
}

func PBMSidecarNotFound(err error) bool {
	return strings.Contains(err.Error(), "container backup-agent is not valid for pod")
}
