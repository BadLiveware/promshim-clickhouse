package promharness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
)

func WriteToRemoteWriteEndpoint(ctx context.Context, client *http.Client, endpoint string, request *prompb.WriteRequest) error {
	payload, err := request.Marshal()
	if err != nil {
		return err
	}
	compressed := snappy.Encode(nil, payload)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return err
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(compressed))
	if err != nil {
		return err
	}
	if parsed.User != nil {
		password, _ := parsed.User.Password()
		httpRequest.SetBasicAuth(parsed.User.Username(), password)
		parsed.User = nil
		httpRequest.URL.User = nil
	}
	httpRequest.Header.Set("Content-Type", "application/x-protobuf")
	httpRequest.Header.Set("Content-Encoding", "snappy")
	httpRequest.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")

	response, err := client.Do(httpRequest)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf("remote write to %s failed: %s: %s", endpoint, response.Status, string(body))
	}
	return nil
}

func WaitForHTTPOK(ctx context.Context, client *http.Client, endpoint string, description string) error {
	deadline, hasDeadline := ctx.Deadline()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode/100 == 2 {
				return nil
			}
		}
		if hasDeadline && time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s at %s", description, endpoint)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for %s at %s: %w", description, endpoint, ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

func WriteManifest(path string, manifest Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}

func ReadManifest(path string) (Manifest, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}
