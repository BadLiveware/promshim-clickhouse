package promshim

import (
	"context"
	"net/http"
	"net/url"

	httpapi "ch-observability/internal/promshim/httpapi"
	"ch-observability/internal/promshim/local"
)

func metadataHTTPRequest(ctx context.Context, req httpapi.MetadataRequest) (*http.Request, *httpapi.APIError) {
	values := url.Values{}
	for _, matcher := range req.Matchers {
		values.Add("match[]", matcher)
	}
	if req.Start != "" {
		values.Set("start", req.Start)
	}
	if req.End != "" {
		values.Set("end", req.End)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "/?"+values.Encode(), nil)
	if err != nil {
		return nil, local.ApiErrorToHTTP(local.NewExecutionErrorf("building metadata request: %v", err))
	}
	return httpReq, nil
}
