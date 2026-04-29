package promharness

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func LoadSeedConfigFromEnv() (SeedConfig, error) {
	stepSeconds := getenvInt("PROM_HARNESS_STEP_SECONDS", 60)
	points := getenvInt("PROM_HARNESS_POINTS", 10)
	cfg := SeedConfig{
		PromRemoteWriteURL:       getenv("PROM_HARNESS_PROM_REMOTE_WRITE_URL", "http://prometheus:9090/api/v1/write"),
		ClickHouseRemoteWriteURL: getenv("PROM_HARNESS_CLICKHOUSE_REMOTE_WRITE_URL", "http://default:otel@clickhouse:9092/write"),
		PromClickRemoteWriteURL:  getenv("PROM_HARNESS_PROMCLICK_REMOTE_WRITE_URL", ""),
		Seed:                     getenvInt64("PROM_HARNESS_SEED", 12345),
		Step:                     time.Duration(stepSeconds) * time.Second,
		Points:                   points,
		ArtifactDir:              getenv("PROM_HARNESS_ARTIFACT_DIR", "/artifacts"),
		DatasetVariants:          parseCSV(getenv("PROM_HARNESS_DATASET_VARIANTS", "")),
	}
	if cfg.Points < 2 {
		return SeedConfig{}, fmt.Errorf("PROM_HARNESS_POINTS must be >= 2")
	}
	if base := os.Getenv("PROM_HARNESS_BASE_UNIX_SECONDS"); base != "" {
		seconds, err := strconv.ParseInt(base, 10, 64)
		if err != nil {
			return SeedConfig{}, fmt.Errorf("invalid PROM_HARNESS_BASE_UNIX_SECONDS: %w", err)
		}
		cfg.BaseTime = time.Unix(seconds, 0).UTC()
	} else {
		latestEnd := time.Now().UTC().Truncate(cfg.Step)
		firstVariantOffset := time.Duration(0)
		if len(cfg.DatasetVariants) > 1 {
			firstVariantOffset = time.Duration(len(cfg.DatasetVariants)-1) * datasetVariantSeparation(cfg)
		}
		cfg.BaseTime = latestEnd.Add(-firstVariantOffset).Add(-time.Duration(cfg.Points-1) * cfg.Step)
	}
	return cfg, nil
}

func LoadCompareConfigFromEnv() (CompareConfig, error) {
	cfg := CompareConfig{
		PrometheusBaseURL:         getenv("PROM_HARNESS_PROM_URL", "http://prometheus:9090"),
		PromshimBaseURL:           getenv("PROM_HARNESS_SHIM_URL", "http://promshim:9090"),
		PromClickBaseURL:          getenv("PROM_HARNESS_PROMCLICK_URL", ""),
		Subjects:                  parseSubjects(getenv("PROM_HARNESS_SUBJECTS", "")),
		DefaultNativeLoweringMode: strings.ToLower(strings.TrimSpace(getenv("PROM_HARNESS_NATIVE_LOWERING_MODE", ""))),
		CorpusPath:                getenv("PROM_HARNESS_CORPUS_PATH", "/workspace/harness/corpus/queries.json"),
		ArtifactDir:               getenv("PROM_HARNESS_ARTIFACT_DIR", "/artifacts"),
		Timeout:                   time.Duration(getenvInt("PROM_HARNESS_TIMEOUT_SECONDS", 30)) * time.Second,
	}
	if cfg.DefaultNativeLoweringMode != "" {
		switch cfg.DefaultNativeLoweringMode {
		case "off", "explain", "shadow", "prefer", "force_supported", "local_pushdown":
		default:
			return CompareConfig{}, fmt.Errorf("invalid PROM_HARNESS_NATIVE_LOWERING_MODE %q", cfg.DefaultNativeLoweringMode)
		}
	}
	return cfg, nil
}

func parseSubjects(raw string) []string {
	return parseCSV(raw)
}

func parseCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		value := strings.ToLower(strings.TrimSpace(part))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func ManifestPath(dir string) string {
	return filepath.Join(dir, "seed-manifest.json")
}

func CompareReportPath(dir string) string {
	return filepath.Join(dir, "compare-report.json")
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvInt64(key string, fallback int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
