package promharness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	planpkg "github.com/BadLiveware/promshim-ch/internal/promshim/plan"
)

type corpusMetadata struct {
	Buckets []struct {
		Name       string   `json:"name"`
		QueryNames []string `json:"queryNames"`
	} `json:"buckets"`
}

func TestLoadQueryCorpusFixtures(t *testing.T) {
	t.Parallel()

	for _, fixture := range []string{"queries.json", "native-lowering-starter.json"} {
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
		case "query_range":
			if query.StepSeconds <= 0 {
				t.Fatalf("%s: range query %q must set stepSeconds > 0", fixture, query.Name)
			}
			if query.EndOffsetSeconds < query.StartOffsetSeconds {
				t.Fatalf("%s: range query %q has end before start", fixture, query.Name)
			}
		default:
			t.Fatalf("%s: query %q has unsupported endpoint %q", fixture, query.Name, query.Endpoint)
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

		switch strings.ToLower(strings.TrimSpace(query.CompareMode)) {
		case "", CompareModeExact, CompareModeStructural:
		default:
			t.Fatalf("%s: query %q has unsupported compareMode %q", fixture, query.Name, query.CompareMode)
		}
	}
}

func corpusFixturePath(name string) string {
	return filepath.Join("..", "..", "harness", "corpus", name)
}
