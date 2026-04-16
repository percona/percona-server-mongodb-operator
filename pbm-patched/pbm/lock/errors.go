package lock

import "fmt"

// ConcurrentOpError means lock was already acquired by another node
type ConcurrentOpError struct {
	Lock LockHeader
}

func (e ConcurrentOpError) Error() string {
	return fmt.Sprintf("another operation is running: %s '%s'", e.Lock.Type, e.Lock.OPID)
}

func (ConcurrentOpError) Is(err error) bool {
	if err == nil {
		return false
	}

	_, ok := err.(ConcurrentOpError) //nolint:errorlint
	return ok
}

// StaleLockError - the lock was already got but the operation seems to be staled (no hb from the node)
type StaleLockError struct {
	Lock LockHeader
}

func (e StaleLockError) Error() string {
	return fmt.Sprintf("was stale lock: %s '%s'", e.Lock.Type, e.Lock.OPID)
}

func (StaleLockError) Is(err error) bool {
	if err == nil {
		return false
	}

	_, ok := err.(StaleLockError) //nolint:errorlint
	return ok
}

// DuplicatedOpError means the operation with the same ID
// alredy had been running
type DuplicatedOpError struct {
	Lock LockHeader
}

func (e DuplicatedOpError) Error() string {
	return fmt.Sprintf("duplicate operation: %s [%s]", e.Lock.OPID, e.Lock.Type)
}

func (DuplicatedOpError) Is(err error) bool {
	if err == nil {
		return false
	}

	_, ok := err.(DuplicatedOpError) //nolint:errorlint
	return ok
}
