package promshim

import (
	"errors"
	"fmt"

	"ch-observability/internal/promshim/exec"
	"ch-observability/internal/promshim/plan"
	"ch-observability/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

type internalErrorKind string

const (
	internalErrorKindBadData     internalErrorKind = "bad_data"
	internalErrorKindUnsupported internalErrorKind = "unsupported"
	internalErrorKindExecution   internalErrorKind = "execution"
)

type internalError interface {
	error
	Kind() internalErrorKind
}

type promshimError struct {
	kind    internalErrorKind
	message string
}

func (e *promshimError) Error() string {
	return e.message
}

func (e *promshimError) Kind() internalErrorKind {
	return e.kind
}

type contextualInternalError struct {
	kind    internalErrorKind
	context string
	cause   error
}

func (e *contextualInternalError) Error() string {
	return fmt.Sprintf("%s: %v", e.context, e.cause)
}

func (e *contextualInternalError) Kind() internalErrorKind {
	return e.kind
}

func (e *contextualInternalError) Unwrap() error {
	return e.cause
}

func newBadDataErrorf(format string, args ...any) error {
	return &promshimError{kind: internalErrorKindBadData, message: fmt.Sprintf(format, args...)}
}

func newUnsupportedErrorf(format string, args ...any) error {
	return &promshimError{kind: internalErrorKindUnsupported, message: fmt.Sprintf(format, args...)}
}

func newExecutionErrorf(format string, args ...any) error {
	return &promshimError{kind: internalErrorKindExecution, message: fmt.Sprintf(format, args...)}
}

func withInternalContext(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	err = normalizeInternalError(err)
	return &contextualInternalError{
		kind:    internalErrorKindOf(err),
		context: fmt.Sprintf(format, args...),
		cause:   err,
	}
}

type planBuildError struct {
	Support plan.SupportResult
	Expr    parser.Expr
	Stage   string
}

func (e *planBuildError) Error() string {
	base := fmt.Sprintf("unsupported PromQL (difficulty=%s): %s", e.Support.Difficulty, e.Support.Reason)
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

func (e *planBuildError) Kind() internalErrorKind {
	return internalErrorKindUnsupported
}

func newPlanBuildError(expr parser.Expr, support plan.SupportResult, stage string) error {
	return &planBuildError{Support: support, Expr: expr, Stage: stage}
}

func normalizeInternalError(err error) error {
	if err == nil {
		return nil
	}

	var internal internalError
	if errors.As(err, &internal) {
		return err
	}

	var queryErr *storage.QueryError
	if asQueryError(err, &queryErr) {
		switch queryErr.ErrorType {
		case string(internalErrorKindBadData):
			return &promshimError{kind: internalErrorKindBadData, message: queryErr.Message}
		case string(internalErrorKindUnsupported):
			return &promshimError{kind: internalErrorKindUnsupported, message: queryErr.Message}
		default:
			return &promshimError{kind: internalErrorKindExecution, message: queryErr.Message}
		}
	}

	return &promshimError{kind: internalErrorKindExecution, message: err.Error()}
}

func internalErrorKindOf(err error) internalErrorKind {
	var internal internalError
	if errors.As(err, &internal) {
		return internal.Kind()
	}
	return internalErrorKindExecution
}

func fromExecError(err error) error {
	if err == nil {
		return nil
	}
	if execErr, ok := err.(*exec.Error); ok {
		switch execErr.Kind {
		case exec.ErrorKindBadData:
			return newBadDataErrorf("%s", execErr.Message)
		case exec.ErrorKindUnsupported:
			return newUnsupportedErrorf("%s", execErr.Message)
		default:
			return newExecutionErrorf("%s", execErr.Message)
		}
	}
	return newExecutionErrorf("%s", err.Error())
}
