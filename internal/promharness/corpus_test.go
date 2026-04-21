package promharness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	planpkg "ch-observability/internal/promshim/plan"
)

type corpusMetadata struct {
	Buckets []struct {
		Name       string   `json:"name"`
		QueryNames []string `json:"queryNames"`
	} `json:"buckets"`
}

func TestLoadQueryCorpusFixtures(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{"queries.json", "native-lowering-starter.json", "path2-measurement-prereqs.json", "phase7-rollout.json", "phase12-harness-variants.json", "phase12-dataset-variants.json", "draft-grafana-top-panel-shortlist.json", "draft-grafana-top-panel-shortlist.dataset-variants.json", "common-dashboard-subset.json"} {
		fixture := fixture
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			queries, err := LoadQueryCorpus(corpusFixturePath(fixture))
			if err != nil {
				t.Fatalf("load corpus %s: %v", fixture, err)
			}
			validateQueryCorpus(t, fixture, queries)
		})
	}
}

func TestDraftGrafanaTopPanelThemeCorporaLoadAndValidate(t *testing.T) {
	t.Parallel()

	for _, themeDir := range []string{"draft-grafana-top-panel-shortlist.themes", "draft-grafana-top-panel-shortlist.dataset-variants.themes"} {
		themeDir := themeDir
		t.Run(themeDir, func(t *testing.T) {
			t.Parallel()
			paths, err := filepath.Glob(filepath.Join("..", "..", "harness", "corpus", themeDir, "*.json"))
			if err != nil {
				t.Fatalf("glob theme corpora: %v", err)
			}
			if len(paths) == 0 {
				t.Fatalf("expected at least one themed dashboard corpus in %s", themeDir)
			}
			for _, path := range paths {
				themeBase := filepath.Base(path)
				if strings.HasSuffix(themeBase, ".metadata.json") || themeBase == "summary.json" {
					continue
				}
				path := path
				baseForRun := filepath.Join(themeDir, themeBase)
				t.Run(baseForRun, func(t *testing.T) {
					t.Parallel()
					queries, err := LoadQueryCorpus(path)
					if err != nil {
						t.Fatalf("load theme corpus %s: %v", baseForRun, err)
					}
					validateQueryCorpus(t, baseForRun, queries)
				})
			}
		})
	}
}

func TestCommonDashboardSubsetMetadataMatchesCorpus(t *testing.T) {
	t.Parallel()

	queries, err := LoadQueryCorpus(corpusFixturePath("common-dashboard-subset.json"))
	if err != nil {
		t.Fatalf("load common dashboard subset: %v", err)
	}
	validateQueryCorpus(t, "common-dashboard-subset.json", queries)

	payload, err := os.ReadFile(corpusFixturePath("common-dashboard-subset.metadata.json"))
	if err != nil {
		t.Fatalf("read common dashboard subset metadata: %v", err)
	}
	var metadata struct {
		IncludedCount      int `json:"includedCount"`
		ExcludedCount      int `json:"excludedCount"`
		ExcludedCandidates []struct {
			Name string `json:"name"`
		} `json:"excludedCandidates"`
	}
	if err := json.Unmarshal(payload, &metadata); err != nil {
		t.Fatalf("unmarshal common dashboard subset metadata: %v", err)
	}
	if metadata.IncludedCount != len(queries) {
		t.Fatalf("includedCount mismatch: metadata=%d corpus=%d", metadata.IncludedCount, len(queries))
	}
	if metadata.ExcludedCount != len(metadata.ExcludedCandidates) {
		t.Fatalf("excludedCount mismatch: metadata=%d excludedCandidates=%d", metadata.ExcludedCount, len(metadata.ExcludedCandidates))
	}

	seen := map[string]struct{}{}
	for _, query := range queries {
		seen[query.Name] = struct{}{}
	}
	for _, excluded := range metadata.ExcludedCandidates {
		if _, ok := seen[excluded.Name]; ok {
			t.Fatalf("excluded candidate %q still present in common dashboard subset", excluded.Name)
		}
	}
}

func TestNativeLoweringStarterMetadataMatchesCorpus(t *testing.T) {
	t.Parallel()

	queries, err := LoadQueryCorpus(corpusFixturePath("native-lowering-starter.json"))
	if err != nil {
		t.Fatalf("load starter corpus: %v", err)
	}
	validateQueryCorpus(t, "native-lowering-starter.json", queries)

	payload, err := os.ReadFile(corpusFixturePath("native-lowering-starter.metadata.json"))
	if err != nil {
		t.Fatalf("read starter metadata: %v", err)
	}
	var metadata corpusMetadata
	if err := json.Unmarshal(payload, &metadata); err != nil {
		t.Fatalf("unmarshal starter metadata: %v", err)
	}
	if len(metadata.Buckets) == 0 {
		t.Fatal("expected starter metadata buckets")
	}

	queryNames := make(map[string]struct{}, len(queries))
	for _, query := range queries {
		queryNames[query.Name] = struct{}{}
	}

	referenced := map[string]string{}
	for _, bucket := range metadata.Buckets {
		if bucket.Name == "" {
			t.Fatal("metadata bucket name must not be empty")
		}
		if len(bucket.QueryNames) == 0 {
			t.Fatalf("metadata bucket %q must reference at least one query", bucket.Name)
		}
		for _, queryName := range bucket.QueryNames {
			if _, ok := queryNames[queryName]; !ok {
				t.Fatalf("metadata bucket %q references unknown query %q", bucket.Name, queryName)
			}
			if previousBucket, dup := referenced[queryName]; dup {
				t.Fatalf("query %q referenced by multiple buckets: %q and %q", queryName, previousBucket, bucket.Name)
			}
			referenced[queryName] = bucket.Name
		}
	}

	if len(referenced) != len(queryNames) {
		missing := make([]string, 0)
		for queryName := range queryNames {
			if _, ok := referenced[queryName]; !ok {
				missing = append(missing, queryName)
			}
		}
		sort.Strings(missing)
		t.Fatalf("starter metadata did not classify all queries, missing=%v", missing)
	}
}

func validateQueryCorpus(t *testing.T, fixture string, queries []QuerySpec) {
	t.Helper()

	if len(queries) == 0 {
		t.Fatalf("%s: expected at least one query", fixture)
	}

	seenNames := map[string]struct{}{}
	for _, query := range queries {
		if strings.TrimSpace(query.Name) == "" {
			t.Fatalf("%s: query name must not be empty", fixture)
		}
		if _, dup := seenNames[query.Name]; dup {
			t.Fatalf("%s: duplicate query name %q", fixture, query.Name)
		}
		seenNames[query.Name] = struct{}{}

		if strings.TrimSpace(query.Query) == "" {
			t.Fatalf("%s: query %q has empty PromQL", fixture, query.Name)
		}
		expectedStatus := strings.ToLower(strings.TrimSpace(query.ExpectedStatus))
		if _, err := planpkg.ParseExpression(query.Query); err != nil && expectedStatus != "error" {
			t.Fatalf("%s: query %q does not parse: %v", fixture, query.Name, err)
		}

		switch query.Endpoint {
		case "query":
			if query.StepSeconds != 0 {
				t.Fatalf("%s: instant query %q must not set stepSeconds", fixture, query.Name)
			}
			if len(query.RangeOffsets) > 0 {
				t.Fatalf("%s: instant query %q must not set rangeOffsets", fixture, query.Name)
			}
			if query.RangeStepMatrix {
				t.Fatalf("%s: instant query %q must not set rangeStepMatrix", fixture, query.Name)
			}
		case "query_range":
			if query.StepSeconds <= 0 {
				t.Fatalf("%s: range query %q must set stepSeconds > 0", fixture, query.Name)
			}
			if query.EndOffsetSeconds < query.StartOffsetSeconds {
				t.Fatalf("%s: range query %q has end before start", fixture, query.Name)
			}
			if len(query.TimeOffsets) > 0 {
				t.Fatalf("%s: range query %q must not set timeOffsets", fixture, query.Name)
			}
		default:
			t.Fatalf("%s: query %q has unsupported endpoint %q", fixture, query.Name, query.Endpoint)
		}

		seenVariantNames := map[string]struct{}{}
		for _, variant := range query.TimeOffsets {
			if name := strings.TrimSpace(variant.Name); name != "" {
				if _, dup := seenVariantNames[name]; dup {
					t.Fatalf("%s: query %q has duplicate timeOffsets variant name %q", fixture, query.Name, name)
				}
				seenVariantNames[name] = struct{}{}
			}
		}
		for _, variant := range query.RangeOffsets {
			if variant.EndOffsetSeconds < variant.StartOffsetSeconds {
				t.Fatalf("%s: query %q has rangeOffsets entry with end before start", fixture, query.Name)
			}
			if name := strings.TrimSpace(variant.Name); name != "" {
				if _, dup := seenVariantNames[name]; dup {
					t.Fatalf("%s: query %q has duplicate variant name %q", fixture, query.Name, name)
				}
				seenVariantNames[name] = struct{}{}
			}
		}

		switch expectedStatus {
		case "", "ok", "error":
		default:
			t.Fatalf("%s: query %q has unsupported expectedStatus %q", fixture, query.Name, query.ExpectedStatus)
		}

		for _, subject := range query.Subjects {
			switch strings.ToLower(strings.TrimSpace(subject)) {
			case "shim", "promclick":
			default:
				t.Fatalf("%s: query %q has unsupported subject %q", fixture, query.Name, subject)
			}
		}
		seenDatasetVariants := map[string]struct{}{}
		for _, variant := range query.DatasetVariants {
			normalized := strings.ToLower(strings.TrimSpace(variant))
			switch normalized {
			case "baseline", "resets_gaps", "churn_stale", "histogram_burst":
			default:
				t.Fatalf("%s: query %q has unsupported datasetVariant %q", fixture, query.Name, variant)
			}
			if _, dup := seenDatasetVariants[normalized]; dup {
				t.Fatalf("%s: query %q repeats datasetVariant %q", fixture, query.Name, variant)
			}
			seenDatasetVariants[normalized] = struct{}{}
		}
		for _, variant := range query.ExcludeDatasetVariants {
			normalized := strings.ToLower(strings.TrimSpace(variant))
			switch normalized {
			case "baseline", "resets_gaps", "churn_stale", "histogram_burst":
			default:
				t.Fatalf("%s: query %q has unsupported excludeDatasetVariant %q", fixture, query.Name, variant)
			}
		}

		switch strings.ToLower(strings.TrimSpace(query.CompareMode)) {
		case "", CompareModeExact, CompareModeStructural:
		default:
			t.Fatalf("%s: query %q has unsupported compareMode %q", fixture, query.Name, query.CompareMode)
		}

		switch strings.ToLower(strings.TrimSpace(query.NativeLoweringMode)) {
		case "", "off", "explain", "shadow", "prefer", "force_supported":
		default:
			t.Fatalf("%s: query %q has unsupported nativeLoweringMode %q", fixture, query.Name, query.NativeLoweringMode)
		}
	}
}

func corpusFixturePath(name string) string {
	return filepath.Join("..", "..", "harness", "corpus", name)
}
