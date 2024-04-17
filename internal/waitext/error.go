package waitext

import "errors"

// NonTransientError is used in conjuction with TODO
// to allow the operation to signal that retrying is pointless
// It works the same as PermanentError in github.com/cenkalti/backoff
type NonTransientError struct {
	Err error
}

func (e *NonTransientError) Error() string {
	return e.Err.Error()
}

func (e *NonTransientError) Unwrap() error {
	return e.Err
}

func (e *NonTransientError) Is(target error) bool {
	// TODO
	var nonTransientError *NonTransientError
	ok := errors.As(target, &nonTransientError)
	return ok
}
