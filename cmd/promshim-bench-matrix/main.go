package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/BadLiveware/promshim-clickhouse/internal/promharness"
)

func main() {
	sweep := flag.String("sweep", "", "Sweep manifest path.")
	sortBy := flag.String("sort-by", "np", "Sort by np, fn, or category.")
	perQuery := flag.Bool("per-query", false, "Emit one row per query.")
	flag.Parse()
	inputs := []promharness.BenchMatrixProfileInput{}
	for _, arg := range flag.Args() {
		profile, path, ok := strings.Cut(arg, ":")
		if !ok || profile == "" || path == "" {
			fmt.Fprintf(os.Stderr, "expected profile:path, got: %s\n", arg)
			os.Exit(1)
		}
		inputs = append(inputs, promharness.BenchMatrixProfileInput{Profile: profile, Path: path})
	}
	out, err := promharness.RenderBenchMatrix(promharness.BenchMatrixOptions{SweepPath: *sweep, SortBy: *sortBy, PerQuery: *perQuery, Profiles: inputs})
	if err != nil {
		fmt.Fprintf(os.Stderr, "promshim-bench-matrix: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(out)
}
