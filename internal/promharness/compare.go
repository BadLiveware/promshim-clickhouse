package promharness

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func RunSeed(ctx context.Context, cfg SeedConfig) (Manifest, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	if err := WaitForHTTPOK(ctx, client, "http://prometheus:9090/-/ready", "prometheus readiness"); err != nil {
		return Manifest{}, err
	}
	if err := WaitForHTTPOK(ctx, client, "http://clickhouse:8123/ping", "clickhouse readiness"); err != nil {
		return Manifest{}, err
	}

	dataset := GenerateDataset(cfg)
	if err := WriteToRemoteWriteEndpoint(ctx, client, cfg.PromRemoteWriteURL, dataset.Request); err != nil {
		return Manifest{}, err
	}
	if err := WriteToRemoteWriteEndpoint(ctx, client, cfg.ClickHouseRemoteWriteURL, dataset.Request); err != nil {
		return Manifest{}, err
	}

	manifest := Manifest{
		Seed:            cfg.Seed,
		BaseUnixSeconds: cfg.BaseTime.Unix(),
		StepSeconds:     int64(cfg.Step / time.Second),
		Points:          cfg.Points,
		SeriesCount:     dataset.SeriesCount,
		SampleCount:     dataset.SampleCount,
		GeneratedAtUTC:  time.Now().UTC().Format(time.RFC3339),
	}
	if err := WriteManifest(ManifestPath(cfg.ArtifactDir), manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func RunCompare(ctx context.Context, cfg CompareConfig) (CompareReport, error) {
	manifest, err := ReadManifest(ManifestPath(cfg.ArtifactDir))
	if err != nil {
		return CompareReport{}, err
	}
	queries, err := LoadQueryCorpus(cfg.CorpusPath)
	if err != nil {
		return CompareReport{}, err
	}
	client := &http.Client{Timeout: cfg.Timeout}
	if err := waitForDatasetAvailability(ctx, client, cfg, manifest); err != nil {
		return CompareReport{}, err
	}

	report := CompareReport{CorpusPath: cfg.CorpusPath, Manifest: manifest, Results: make([]QueryComparison, 0, len(queries))}
	var firstErr error
	for _, query := range queries {
		promResult, err := QueryAndNormalize(client, cfg.PrometheusBaseURL, manifest, query)
		if err != nil {
			report.Results = append(report.Results, QueryComparison{Name: query.Name, Query: query.Query, Status: "error", Detail: err.Error()})
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		shimResult, err := QueryAndNormalize(client, cfg.PromshimBaseURL, manifest, query)
		if err != nil {
			report.Results = append(report.Results, QueryComparison{Name: query.Name, Query: query.Query, Status: "error", Detail: err.Error()})
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := CompareNormalizedResults(promResult, shimResult); err != nil {
			report.Results = append(report.Results, QueryComparison{Name: query.Name, Query: query.Query, Status: "diff", Detail: err.Error()})
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		report.Results = append(report.Results, QueryComparison{Name: query.Name, Query: query.Query, Status: "ok"})
	}
	if err := writeCompareReport(cfg.ArtifactDir, report); err != nil && firstErr == nil {
		firstErr = err
	}
	if firstErr != nil {
		return report, firstErr
	}
	return report, nil
}

func waitForDatasetAvailability(ctx context.Context, client *http.Client, cfg CompareConfig, manifest Manifest) error {
	deadline, hasDeadline := ctx.Deadline()
	probe := QuerySpec{Name: "probe", Endpoint: "query", Query: `harness_up{job="api"}`, TimeOffsetSeconds: int64((manifest.Points - 1)) * manifest.StepSeconds}
	for {
		promResult, promErr := QueryAndNormalize(client, cfg.PrometheusBaseURL, manifest, probe)
		shimResult, shimErr := QueryAndNormalize(client, cfg.PromshimBaseURL, manifest, probe)
		if promErr == nil && shimErr == nil && len(promResult.Vector) > 0 && len(shimResult.Vector) > 0 {
			return nil
		}
		if hasDeadline && time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for seeded dataset availability: prom=%v shim=%v", promErr, shimErr)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for seeded dataset availability: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

func writeCompareReport(dir string, report CompareReport) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(filepath.Join(dir, "compare-report.json"), payload, 0o644)
}

func CompareNormalizedResults(left, right normalizedResult) error {
	if left.ResultType != right.ResultType {
		return fmt.Errorf("resultType mismatch: %s vs %s", left.ResultType, right.ResultType)
	}
	switch left.ResultType {
	case "scalar":
		if !equalFloat(left.Scalar.Timestamp, right.Scalar.Timestamp) || !equalFloat(left.Scalar.Value, right.Scalar.Value) {
			return fmt.Errorf("scalar mismatch: %#v vs %#v", left.Scalar, right.Scalar)
		}
	case "vector":
		if len(left.Vector) != len(right.Vector) {
			return fmt.Errorf("vector length mismatch: %d vs %d", len(left.Vector), len(right.Vector))
		}
		for i := range left.Vector {
			if labelKey(left.Vector[i].Metric) != labelKey(right.Vector[i].Metric) || !equalFloat(left.Vector[i].Timestamp, right.Vector[i].Timestamp) || !equalFloat(left.Vector[i].Value, right.Vector[i].Value) {
				return fmt.Errorf("vector row mismatch at %d: %#v vs %#v", i, left.Vector[i], right.Vector[i])
			}
		}
	case "matrix":
		if len(left.Matrix) != len(right.Matrix) {
			return fmt.Errorf("matrix series length mismatch: %d vs %d", len(left.Matrix), len(right.Matrix))
		}
		for i := range left.Matrix {
			if labelKey(left.Matrix[i].Metric) != labelKey(right.Matrix[i].Metric) {
				return fmt.Errorf("matrix series labels mismatch at %d: %#v vs %#v", i, left.Matrix[i].Metric, right.Matrix[i].Metric)
			}
			if len(left.Matrix[i].Values) != len(right.Matrix[i].Values) {
				return fmt.Errorf("matrix points length mismatch at %d: %d vs %d", i, len(left.Matrix[i].Values), len(right.Matrix[i].Values))
			}
			for j := range left.Matrix[i].Values {
				if !equalFloat(left.Matrix[i].Values[j].Timestamp, right.Matrix[i].Values[j].Timestamp) || !equalFloat(left.Matrix[i].Values[j].Value, right.Matrix[i].Values[j].Value) {
					return fmt.Errorf("matrix point mismatch at series %d point %d: %#v vs %#v", i, j, left.Matrix[i].Values[j], right.Matrix[i].Values[j])
				}
			}
		}
	default:
		return fmt.Errorf("unsupported result type %q", left.ResultType)
	}
	return nil
}

func equalFloat(left, right float64) bool {
	if math.IsNaN(left) && math.IsNaN(right) {
		return true
	}
	return math.Abs(left-right) <= 1e-9
}
