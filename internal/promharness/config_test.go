package promharness

import "testing"

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
