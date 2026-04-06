package provider

import "fmt"

type ErrorKind string

const (
	ErrorKindUnknown       ErrorKind = "unknown"
	ErrorKindRetryable     ErrorKind = "retryable"
	ErrorKindQuotaExceeded ErrorKind = "quota_exceeded"
	ErrorKindFatal         ErrorKind = "fatal"
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

func NewQuotaExceededError(err error) error {
	return &ProviderError{Kind: ErrorKindQuotaExceeded, Err: err}
}

func NewFatalError(err error) error {
	return &ProviderError{Kind: ErrorKindFatal, Err: err}
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
		return true
	case "retryable":
		return kind == ErrorKindRetryable || kind == ErrorKindQuotaExceeded
	case "quota_exceeded":
		return kind == ErrorKindQuotaExceeded
	default:
		panic(fmt.Sprintf("unsupported fallback condition %q", condition))
	}
}
