package promharness

import (
	"testing"
	"time"
)

func TestLoadSeedConfigFromEnvKeepsLastDatasetVariantOutOfFuture(t *testing.T) {
	t.Setenv("PROM_HARNESS_DATASET_VARIANTS", "baseline,resets_gaps,churn_stale,histogram_burst")
	t.Setenv("PROM_HARNESS_STEP_SECONDS", "60")
	t.Setenv("PROM_HARNESS_POINTS", "10")

	before := time.Now().UTC().Truncate(time.Minute)
	cfg, err := LoadSeedConfigFromEnv()
	if err != nil {
		t.Fatalf("load seed config: %v", err)
	}
	after := time.Now().UTC().Truncate(time.Minute)

	lastVariantBase := cfg.BaseTime.Add(time.Duration(len(cfg.DatasetVariants)-1) * datasetVariantSeparation(cfg))
	lastVariantEnd := lastVariantBase.Add(time.Duration(cfg.Points-1) * cfg.Step)
	if lastVariantEnd.Before(before) || lastVariantEnd.After(after) {
		t.Fatalf("last variant end = %s, want between %s and %s", lastVariantEnd, before, after)
	}
}

func TestLoadCompareConfigFromEnvParsesDefaultNativeLoweringMode(t *testing.T) {
	t.Setenv("PROM_HARNESS_NATIVE_LOWERING_MODE", "force_supported")
	cfg, err := LoadCompareConfigFromEnv()
	if err != nil {
		t.Fatalf("expected config to load, got %v", err)
	}
	if cfg.DefaultNativeLoweringMode != "force_supported" {
		t.Fatalf("expected force_supported default mode, got %#v", cfg)
	}
}

func TestLoadCompareConfigFromEnvRejectsInvalidDefaultNativeLoweringMode(t *testing.T) {
	t.Setenv("PROM_HARNESS_NATIVE_LOWERING_MODE", "definitely_not_valid")
	if _, err := LoadCompareConfigFromEnv(); err == nil {
		t.Fatal("expected invalid default native lowering mode to fail")
	}
}
