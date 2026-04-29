package promharness

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func LoadQueryCorpus(path string) ([]QuerySpec, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var queries []QuerySpec
	if err := json.Unmarshal(payload, &queries); err != nil {
		return nil, err
	}
	for i := range queries {
		if err := validateQuerySpec(queries[i]); err != nil {
			return nil, fmt.Errorf("invalid query corpus entry #%d (%q): %w", i+1, queries[i].Name, err)
		}
	}
	return queries, nil
}

func validateQuerySpec(spec QuerySpec) error {
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("name must not be empty")
	}
	if strings.TrimSpace(spec.Query) == "" {
		return fmt.Errorf("query must not be empty")
	}
	switch spec.Endpoint {
	case "query":
		return nil
	case "query_range":
		if spec.EndOffsetSeconds < spec.StartOffsetSeconds {
			return fmt.Errorf("query_range requires endOffsetSeconds >= startOffsetSeconds (got start=%d end=%d)", spec.StartOffsetSeconds, spec.EndOffsetSeconds)
		}
		if spec.StepSeconds <= 0 {
			return fmt.Errorf("query_range requires stepSeconds > 0 (got %d)", spec.StepSeconds)
		}
		return nil
	default:
		return fmt.Errorf("unsupported endpoint %q", spec.Endpoint)
	}
}
