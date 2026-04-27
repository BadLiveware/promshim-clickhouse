package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/BadLiveware/promshim-clickhouse/internal/promharness"
)

type benchMatrixCLIOptions struct {
	Sweep    string
	SortBy   string
	PerQuery bool
	Inputs   []promharness.BenchMatrixProfileInput
}

func main() {
	opts, err := parseBenchMatrixArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out, err := promharness.RenderBenchMatrix(promharness.BenchMatrixOptions{SweepPath: opts.Sweep, SortBy: opts.SortBy, PerQuery: opts.PerQuery, Profiles: opts.Inputs})
	if err != nil {
		fmt.Fprintf(os.Stderr, "promshim-bench-matrix: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(out)
}

func parseBenchMatrixArgs(args []string) (benchMatrixCLIOptions, error) {
	flags := flag.NewFlagSet("promshim-bench-matrix", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	sweep := flags.String("sweep", "", "Sweep manifest path.")
	sortBy := flags.String("sort-by", "np", "Sort by np, fn, or category.")
	perQuery := flags.Bool("per-query", false, "Emit one row per query.")

	flagArgs, positional, err := reorderBenchMatrixArgs(args)
	if err != nil {
		return benchMatrixCLIOptions{}, err
	}
	if err := flags.Parse(append(flagArgs, positional...)); err != nil {
		return benchMatrixCLIOptions{}, err
	}
	inputs := []promharness.BenchMatrixProfileInput{}
	for _, arg := range flags.Args() {
		profile, path, ok := strings.Cut(arg, ":")
		if !ok || profile == "" || path == "" {
			return benchMatrixCLIOptions{}, fmt.Errorf("expected profile:path, got: %s", arg)
		}
		inputs = append(inputs, promharness.BenchMatrixProfileInput{Profile: profile, Path: path})
	}
	return benchMatrixCLIOptions{Sweep: *sweep, SortBy: *sortBy, PerQuery: *perQuery, Inputs: inputs}, nil
}

func reorderBenchMatrixArgs(args []string) ([]string, []string, error) {
	flagArgs := []string{}
	positionals := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--per-query" || arg == "-h" || arg == "--help":
			flagArgs = append(flagArgs, arg)
		case arg == "--sweep" || arg == "--sort-by":
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("%s requires a value", arg)
			}
			flagArgs = append(flagArgs, arg, args[i+1])
			i++
		case strings.HasPrefix(arg, "--sweep=") || strings.HasPrefix(arg, "--sort-by="):
			flagArgs = append(flagArgs, arg)
		default:
			positionals = append(positionals, arg)
		}
	}
	return flagArgs, positionals, nil
}
