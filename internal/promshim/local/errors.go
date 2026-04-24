package local

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	httpapi "github.com/BadLiveware/promshim-ch/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-ch/internal/promshim/local/exec"
	"github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

// internalErrorKind is an alias for logical.ErrorKind so that error values
// produced by logical/build.go (which implement Kind() logical.ErrorKind)
// also satisfy the local internalError interface.
type internalErrorKind = logical.ErrorKind

const (
	internalErrorKindBadData     internalErrorKind = logical.ErrorKindBadData
	internalErrorKindUnsupported internalErrorKind = logical.ErrorKindUnsupported
	internalErrorKindExecution   internalErrorKind = logical.ErrorKindExecution
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

func NewBadDataErrorf(format string, args ...any) error {
	return &promshimError{kind: internalErrorKindBadData, message: fmt.Sprintf(format, args...)}
}

func NewUnsupportedErrorf(format string, args ...any) error {
	return &promshimError{kind: internalErrorKindUnsupported, message: fmt.Sprintf(format, args...)}
}

func NewExecutionErrorf(format string, args ...any) error {
	return &promshimError{kind: internalErrorKindExecution, message: fmt.Sprintf(format, args...)}
}

func WithInternalContext(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	err = NormalizeInternalError(err)
	return &contextualInternalError{
		kind:    internalErrorKindOf(err),
		context: fmt.Sprintf(format, args...),
		cause:   err,
	}
}

// PlanBuildError is a type alias for logical.BuildError so that existing
// callers (e.g. errors_http_test.go) continue to compile.
type PlanBuildError = logical.BuildError

// NewPlanBuildError constructs a PlanBuildError for an unsupported expression.
func NewPlanBuildError(expr parser.Expr, support logical.SupportResult, stage string) error {
	return logical.NewBuildError(expr, support, stage)
}

func NormalizeInternalError(err error) error {
	if err == nil {
		return nil
	}

	var internal internalError
	if errors.As(err, &internal) {
		return err
	}

	var queryErr *storage.QueryError
	if asQueryError(err, &queryErr) {
		if normalized := normalizeQueryError(queryErr); normalized != nil {
			return normalized
		}
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

func normalizeQueryError(err *storage.QueryError) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Message, "found duplicate series for the match group on the ") {
		// ClickHouse surfaces the SQL-side uniqueness guard as an execution error,
		// but Prometheus classifies this join shape as bad-data and reports the
		// higher-level vector-matching contract instead.
		return NewBadDataErrorf("multiple matches for labels: many-to-one matching must be explicit (group_left/group_right)")
	}
	return nil
}

func internalErrorKindOf(err error) internalErrorKind {
	var internal internalError
	if errors.As(err, &internal) {
		return internal.Kind()
	}
	return internalErrorKindExecution
}

func IsBadDataError(err error) bool {
	return internalErrorKindOf(err) == internalErrorKindBadData
}

func FromExecError(err error) error {
	if err == nil {
		return nil
	}
	if execErr, ok := err.(*exec.Error); ok {
		switch execErr.Kind {
		case exec.ErrorKindBadData:
			return NewBadDataErrorf("%s", execErr.Message)
		case exec.ErrorKindUnsupported:
			return NewUnsupportedErrorf("%s", execErr.Message)
		default:
			return NewExecutionErrorf("%s", execErr.Message)
		}
	}
	return NewExecutionErrorf("%s", err.Error())
}

type APIError struct {
	StatusCode int    `json:"-"`
	ErrorType  string `json:"errorType"`
	Error      string `json:"error"`
}

func apiErrorFromInternal(err error) APIError {
	err = NormalizeInternalError(err)
	kind := internalErrorKindOf(err)
	statusCode := http.StatusBadGateway
	switch kind {
	case internalErrorKindBadData:
		statusCode = http.StatusBadRequest
	case internalErrorKindUnsupported:
		statusCode = http.StatusUnprocessableEntity
	case internalErrorKindExecution:
		statusCode = http.StatusBadGateway
	}
	return APIError{StatusCode: statusCode, ErrorType: string(kind), Error: userFacingErrorMessage(err)}
}

func userFacingErrorMessage(err error) string {
	err = NormalizeInternalError(err)
	if internalErrorKindOf(err) == internalErrorKindExecution {
		return err.Error()
	}
	var buildErr *PlanBuildError
	if errors.As(err, &buildErr) {
		return buildErr.UserMessage()
	}
	root := err
	for {
		next := errors.Unwrap(root)
		if next == nil {
			break
		}
		root = next
	}
	return root.Error()
}

func asQueryError(err error, target **storage.QueryError) bool {
	queryErr, ok := err.(*storage.QueryError)
	if ok {
		*target = queryErr
	}
	return ok
}

func ApiErrorToHTTP(err error) *httpapi.APIError {
	return ApiErrorPtr(ToHTTPAPIError(apiErrorFromInternal(err)))
}

func ToHTTPAPIError(err APIError) httpapi.APIError {
	return httpapi.APIError{StatusCode: err.StatusCode, ErrorType: err.ErrorType, Error: err.Error}
}

func BadRequestHTTPError(message string) *httpapi.APIError {
	return &httpapi.APIError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: message}
}

func ApiErrorPtr(err httpapi.APIError) *httpapi.APIError {
	return &err
}
