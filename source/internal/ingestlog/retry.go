package ingestlog

import (
	"context"
	"errors"
)

type retryableError struct{ error }

func (e retryableError) Unwrap() error   { return e.error }
func (e retryableError) Temporary() bool { return true }

// Retryable marks a transient storage failure. Callers retain the same input
// and identity until a retry commits; validation and ownership errors stay fatal.
func Retryable(err error) error {
	if err == nil || IsRetryable(err) {
		return err
	}
	return retryableError{err}
}

func IsRetryable(err error) bool {
	var temporary interface{ Temporary() bool }
	return errors.As(err, &temporary) && temporary.Temporary()
}

// A joined shutdown error can contain both cancellation and a failed durable
// write. Only an error tree consisting entirely of cancellation is benign.
func IsCancellationOnly(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !IsCancellationOnly(cause) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return IsCancellationOnly(wrapped.Unwrap())
	}
	return errors.Is(err, context.Canceled)
}
