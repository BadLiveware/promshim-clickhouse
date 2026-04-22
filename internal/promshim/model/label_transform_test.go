package model

import "testing"

func TestBuildLabelReplaceConfigAcceptsPrometheus3UTF8DestinationLabel(t *testing.T) {
	cfg, err := BuildLabelReplaceConfig("~invalid", "$1", "src", "(.*)")
	if err != nil {
		t.Fatalf("expected UTF-8 destination label to be accepted, got error: %v", err)
	}
	if cfg.Dst != "~invalid" {
		t.Fatalf("unexpected destination label: %#v", cfg)
	}
}

func TestBuildLabelJoinConfigAcceptsPrometheus3UTF8DestinationLabel(t *testing.T) {
	cfg, err := BuildLabelJoinConfig("~invalid", "-", []string{"instance"})
	if err != nil {
		t.Fatalf("expected UTF-8 destination label to be accepted, got error: %v", err)
	}
	if cfg.Dst != "~invalid" {
		t.Fatalf("unexpected destination label: %#v", cfg)
	}
}
