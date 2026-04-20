package promshim

import (
	"context"
	"net/http"
	"net/url"

	httpapi "github.com/BadLiveware/promshim-ch/internal/promshim/httpapi"
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
		return nil, apiErrorToHTTP(newExecutionErrorf("building metadata request: %v", err))
	}
	return httpReq, nil
}
