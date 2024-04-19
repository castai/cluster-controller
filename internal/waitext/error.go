package waitext

// NonTransientError is used in conjuction with Retry or RetryWithContext
// to allow the operation to signal that retrying is pointless
// It works the same as PermanentError in github.com/cenkalti/backoff
type NonTransientError struct {
	Err error
}

func NewNonTransientError(err error) *NonTransientError {
	return &NonTransientError{Err: err}
}

var _ error = &NonTransientError{}

func (e *NonTransientError) Error() string {
	return e.Err.Error()
}

func (e *NonTransientError) Unwrap() error {
	return e.Err
}
