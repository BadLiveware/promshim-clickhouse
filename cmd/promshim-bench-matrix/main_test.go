package main

import "testing"

func TestParseBenchMatrixArgsAllowsFlagsAfterPositionals(t *testing.T) {
	opts, err := parseBenchMatrixArgs([]string{"7d:foo.json", "--sort-by", "fn", "--per-query", "30d:bar.json"})
	if err != nil {
		t.Fatalf("parseBenchMatrixArgs: %v", err)
	}
	if opts.SortBy != "fn" || !opts.PerQuery {
		t.Fatalf("opts = %#v", opts)
	}
	if len(opts.Inputs) != 2 || opts.Inputs[0].Profile != "7d" || opts.Inputs[1].Path != "bar.json" {
		t.Fatalf("inputs = %#v", opts.Inputs)
	}
}

func TestParseBenchMatrixArgsRejectsBadPositional(t *testing.T) {
	_, err := parseBenchMatrixArgs([]string{"7d:foo.json", "--sort-by"})
	if err == nil {
		t.Fatal("expected error for missing --sort-by value")
	}
}
