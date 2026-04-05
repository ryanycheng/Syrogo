package execution

import "fmt"

type ErrorKind string

const (
	ErrorKindUnknown       ErrorKind = "unknown"
	ErrorKindRetryable     ErrorKind = "retryable"
	ErrorKindQuotaExceeded ErrorKind = "quota_exceeded"
	ErrorKindFatal         ErrorKind = "fatal"
)

type ExecutionError struct {
	Kind ErrorKind
	Err  error
}

func (e *ExecutionError) Error() string {
	if e == nil || e.Err == nil {
		return "execution error"
	}
	return e.Err.Error()
}

func (e *ExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewRetryableError(err error) error {
	return &ExecutionError{Kind: ErrorKindRetryable, Err: err}
}

func NewQuotaExceededError(err error) error {
	return &ExecutionError{Kind: ErrorKindQuotaExceeded, Err: err}
}

func NewFatalError(err error) error {
	return &ExecutionError{Kind: ErrorKindFatal, Err: err}
}

func classifyError(err error) ErrorKind {
	if err == nil {
		return ErrorKindUnknown
	}

	var execErr *ExecutionError
	if ok := AsExecutionError(err, &execErr); ok && execErr != nil {
		return execErr.Kind
	}

	return ErrorKindUnknown
}

func AsExecutionError(err error, target **ExecutionError) bool {
	if err == nil {
		return false
	}

	execErr, ok := err.(*ExecutionError)
	if ok {
		*target = execErr
		return true
	}

	unwrapper, ok := err.(interface{ Unwrap() error })
	if !ok {
		return false
	}

	return AsExecutionError(unwrapper.Unwrap(), target)
}

func shouldFallback(condition runtimeFallbackCondition, kind ErrorKind, isLast bool) bool {
	if isLast {
		return false
	}

	switch condition {
	case runtimeFallbackAlways:
		return true
	case runtimeFallbackOnRetryable:
		return kind == ErrorKindRetryable || kind == ErrorKindQuotaExceeded
	case runtimeFallbackOnQuotaExceeded:
		return kind == ErrorKindQuotaExceeded
	default:
		return false
	}
}

type runtimeFallbackCondition string

const (
	runtimeFallbackAlways          runtimeFallbackCondition = "always"
	runtimeFallbackOnRetryable     runtimeFallbackCondition = "retryable"
	runtimeFallbackOnQuotaExceeded runtimeFallbackCondition = "quota_exceeded"
)

func normalizeFallbackCondition(condition string) runtimeFallbackCondition {
	switch condition {
	case "", string(runtimeFallbackAlways):
		return runtimeFallbackAlways
	case string(runtimeFallbackOnRetryable):
		return runtimeFallbackOnRetryable
	case string(runtimeFallbackOnQuotaExceeded):
		return runtimeFallbackOnQuotaExceeded
	default:
		panic(fmt.Sprintf("unsupported fallback condition %q", condition))
	}
}
