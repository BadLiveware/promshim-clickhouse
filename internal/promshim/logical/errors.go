package logical

import (
	"fmt"

	"github.com/prometheus/prometheus/promql/parser"
)

// ErrorKind classifies plan-build errors for HTTP status mapping.
type ErrorKind string

const (
	ErrorKindBadData     ErrorKind = "bad_data"
	ErrorKindUnsupported ErrorKind = "unsupported"
	ErrorKindExecution   ErrorKind = "execution"
)

// BuildError is returned when the builder cannot translate a PromQL expression
// to the logical IR. It carries the expression, analysis result, and the
// planning stage at which the failure occurred.
type BuildError struct {
	Support SupportResult
	Expr    parser.Expr
	Stage   string
}

func (e *BuildError) Error() string {
	base := e.UserMessage()
	if e.Expr == nil && e.Stage == "" {
		return fmt.Sprintf("planner cannot build plan: %s", base)
	}
	if e.Expr == nil {
		return fmt.Sprintf("planner cannot build plan during %s: %s", e.Stage, base)
	}
	if e.Stage == "" {
		return fmt.Sprintf("planner cannot build plan for %T %q: %s", e.Expr, e.Expr.String(), base)
	}
	return fmt.Sprintf("planner cannot build plan during %s for %T %q: %s", e.Stage, e.Expr, e.Expr.String(), base)
}

func (e *BuildError) UserMessage() string {
	return fmt.Sprintf("unsupported PromQL (difficulty=%s): %s", e.Support.Difficulty, e.Support.Reason)
}

// ErrorKind implements the classifier interface used by the HTTP layer.
func (e *BuildError) ErrorKind() ErrorKind {
	return ErrorKindUnsupported
}

// Kind is an alias for ErrorKind, satisfying the local.internalError interface
// (which uses Kind() for compatibility with the existing HTTP error layer).
func (e *BuildError) Kind() ErrorKind {
	return ErrorKindUnsupported
}

// buildSimpleError is an unexported typed error used within the builder.
type buildSimpleError struct {
	kind    ErrorKind
	message string
}

func (e *buildSimpleError) Error() string {
	return e.message
}

func (e *buildSimpleError) ErrorKind() ErrorKind {
	return e.kind
}

func (e *buildSimpleError) Kind() ErrorKind {
	return e.kind
}

// buildContextError wraps an inner error with additional context.
type buildContextError struct {
	kind    ErrorKind
	context string
	cause   error
}

func (e *buildContextError) Error() string {
	return fmt.Sprintf("%s: %v", e.context, e.cause)
}

func (e *buildContextError) ErrorKind() ErrorKind {
	return e.kind
}

func (e *buildContextError) Kind() ErrorKind {
	return e.kind
}

func (e *buildContextError) Unwrap() error {
	return e.cause
}

// BuildErrorKind returns the ErrorKind of an error produced by the builder.
// Returns ErrorKindExecution for unclassified errors.
func BuildErrorKind(err error) ErrorKind {
	type classifier interface {
		ErrorKind() ErrorKind
	}
	if c, ok := err.(classifier); ok {
		return c.ErrorKind()
	}
	return ErrorKindExecution
}

// NewBuildError constructs a BuildError for an unsupported expression.
func NewBuildError(expr parser.Expr, support SupportResult, stage string) error {
	return &BuildError{Support: support, Expr: expr, Stage: stage}
}

// NewBadDataErrorf constructs a bad-data error (HTTP 400).
func NewBadDataErrorf(format string, args ...any) error {
	return &buildSimpleError{kind: ErrorKindBadData, message: fmt.Sprintf(format, args...)}
}

// NewUnsupportedErrorf constructs an unsupported error (HTTP 422).
func NewUnsupportedErrorf(format string, args ...any) error {
	return &buildSimpleError{kind: ErrorKindUnsupported, message: fmt.Sprintf(format, args...)}
}

// withBuildContext wraps err with additional context, preserving the
// ErrorKind of the original error. Unlike local.WithInternalContext it does
// not call NormalizeInternalError — errors produced inside the builder are
// already properly typed, so normalization is a no-op there anyway.
func withBuildContext(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return &buildContextError{
		kind:    BuildErrorKind(err),
		context: fmt.Sprintf(format, args...),
		cause:   err,
	}
}
