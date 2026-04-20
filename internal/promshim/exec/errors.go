package exec

import "fmt"

type ErrorKind string

const (
	ErrorKindBadData     ErrorKind = "bad_data"
	ErrorKindUnsupported ErrorKind = "unsupported"
	ErrorKindExecution   ErrorKind = "execution"
)

type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string { return e.Message }

func badDataf(format string, args ...any) error {
	return &Error{Kind: ErrorKindBadData, Message: sprintf(format, args...)}
}

func unsupportedf(format string, args ...any) error {
	return &Error{Kind: ErrorKindUnsupported, Message: sprintf(format, args...)}
}

func executionf(format string, args ...any) error {
	return &Error{Kind: ErrorKindExecution, Message: sprintf(format, args...)}
}

func sprintf(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}
