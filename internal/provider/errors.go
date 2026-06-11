package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
)

type ErrorKind string

const (
	ErrorKindUnknown               ErrorKind = "unknown"
	ErrorKindRetryable             ErrorKind = "retryable"
	ErrorKindTimeout               ErrorKind = "timeout"
	ErrorKindQuotaExceeded         ErrorKind = "quota_exceeded"
	ErrorKindUpstreamServerError   ErrorKind = "upstream_server_error"
	ErrorKindAuthFailed            ErrorKind = "auth_failed"
	ErrorKindCapabilityUnsupported ErrorKind = "capability_unsupported"
	ErrorKindFatal                 ErrorKind = "fatal"
)

type ProviderError struct {
	Kind ErrorKind
	Err  error
}

func (e *ProviderError) Error() string {
	if e == nil || e.Err == nil {
		return "provider error"
	}
	return e.Err.Error()
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewRetryableError(err error) error {
	return &ProviderError{Kind: ErrorKindRetryable, Err: err}
}

func NewTimeoutError(err error) error {
	return &ProviderError{Kind: ErrorKindTimeout, Err: err}
}

func NewQuotaExceededError(err error) error {
	return &ProviderError{Kind: ErrorKindQuotaExceeded, Err: err}
}

func NewUpstreamServerError(err error) error {
	return &ProviderError{Kind: ErrorKindUpstreamServerError, Err: err}
}

func NewAuthFailedError(err error) error {
	return &ProviderError{Kind: ErrorKindAuthFailed, Err: err}
}

func NewCapabilityUnsupportedError(err error) error {
	return &ProviderError{Kind: ErrorKindCapabilityUnsupported, Err: err}
}

func NewFatalError(err error) error {
	return &ProviderError{Kind: ErrorKindFatal, Err: err}
}

func NewTransientError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return NewTimeoutError(err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return NewTimeoutError(err)
	}
	return NewRetryableError(err)
}

func NormalizeError(err error) ErrorKind {
	if err == nil {
		return ErrorKindUnknown
	}

	var providerErr *ProviderError
	if AsProviderError(err, &providerErr) && providerErr != nil {
		return providerErr.Kind
	}

	return ErrorKindUnknown
}

func AsProviderError(err error, target **ProviderError) bool {
	if err == nil {
		return false
	}

	providerErr, ok := err.(*ProviderError)
	if ok {
		*target = providerErr
		return true
	}

	unwrapper, ok := err.(interface{ Unwrap() error })
	if !ok {
		return false
	}

	return AsProviderError(unwrapper.Unwrap(), target)
}

func FallbackAllowed(condition string, kind ErrorKind, isLast bool) bool {
	if isLast {
		return false
	}

	switch condition {
	case "", "always":
		return isRecoverable(kind)
	case "retryable":
		return isRecoverable(kind)
	case "quota_exceeded":
		return kind == ErrorKindQuotaExceeded
	default:
		panic(fmt.Sprintf("unsupported fallback condition %q", condition))
	}
}

func isRecoverable(kind ErrorKind) bool {
	switch kind {
	case ErrorKindRetryable, ErrorKindTimeout, ErrorKindQuotaExceeded, ErrorKindUpstreamServerError:
		return true
	default:
		return false
	}
}
