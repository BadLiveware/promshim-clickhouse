package promshim

import (
	"net/http"

	httpapi "github.com/BadLiveware/promshim-ch/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

type apiError struct {
	StatusCode int    `json:"-"`
	ErrorType  string `json:"errorType"`
	Error      string `json:"error"`
}

func apiErrorFromInternal(err error) apiError {
	err = normalizeInternalError(err)
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
	return apiError{StatusCode: statusCode, ErrorType: string(kind), Error: err.Error()}
}

func asQueryError(err error, target **storage.QueryError) bool {
	queryErr, ok := err.(*storage.QueryError)
	if ok {
		*target = queryErr
	}
	return ok
}

func apiErrorToHTTP(err error) *httpapi.APIError {
	return apiErrorPtr(toHTTPAPIError(apiErrorFromInternal(err)))
}

func toHTTPAPIError(err apiError) httpapi.APIError {
	return httpapi.APIError{StatusCode: err.StatusCode, ErrorType: err.ErrorType, Error: err.Error}
}

func badRequestHTTPError(message string) *httpapi.APIError {
	return &httpapi.APIError{StatusCode: http.StatusBadRequest, ErrorType: "bad_data", Error: message}
}

func apiErrorPtr(err httpapi.APIError) *httpapi.APIError {
	return &err
}
