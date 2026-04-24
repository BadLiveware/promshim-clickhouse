package promharness

import "time"

type SeedConfig struct {
	PromRemoteWriteURL       string
	ClickHouseRemoteWriteURL string
	// PromClickRemoteWriteURL is optional. When empty, PromClick seeding is
	// skipped so the harness still runs when PromClick is not deployed.
	PromClickRemoteWriteURL string
	Seed                    int64
	Step                    time.Duration
	Points                  int
	BaseTime                time.Time
	ArtifactDir             string
	DatasetVariant          string
	DatasetVariants         []string
}

type Manifest struct {
	Seed            int64      `json:"seed"`
	BaseUnixSeconds int64      `json:"baseUnixSeconds"`
	StepSeconds     int64      `json:"stepSeconds"`
	Points          int        `json:"points"`
	SeriesCount     int        `json:"seriesCount"`
	SampleCount     int        `json:"sampleCount"`
	GeneratedAtUTC  string     `json:"generatedAtUtc"`
	DatasetVariant  string     `json:"datasetVariant,omitempty"`
	Variants        []Manifest `json:"variants,omitempty"`
}

type CompareConfig struct {
	PrometheusBaseURL string
	PromshimBaseURL   string
	// PromClickBaseURL is optional. When empty, PromClick is not queried and
	// the compare pass runs as a 2-way comparison (Prometheus vs shim) as
	// before.
	PromClickBaseURL string
	// Subjects restricts comparison to a subset of configured subjects.
	// Supported names today: "shim", "promclick". Empty means all configured
	// subjects.
	Subjects []string
	// DefaultNativeLoweringMode applies a harness-wide native lowering mode to
	// every query row that does not already set NativeLoweringMode explicitly.
	// This is the ergonomic switch used by native-only measurement runs.
	DefaultNativeLoweringMode string
	CorpusPath                string
	ArtifactDir               string
	Timeout                   time.Duration
}

type QueryInstantVariantSpec struct {
	Name              string `json:"name,omitempty"`
	TimeOffsetSeconds int64  `json:"timeOffsetSeconds"`
}

type QueryRangeVariantSpec struct {
	Name               string `json:"name,omitempty"`
	StartOffsetSeconds int64  `json:"startOffsetSeconds"`
	EndOffsetSeconds   int64  `json:"endOffsetSeconds"`
}

type TargetPromP50MS struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

type QuerySpec struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Query    string `json:"query"`
	// Category groups related queries for cross-profile matrix rendering
	// (scripts/bench-matrix.sh). Free-form; suggested values follow the
	// "{shape}_{window}" convention — e.g. "instant_rate_short",
	// "range_histogram_quantile", "selector_plain".
	Category              string                    `json:"category,omitempty"`
	TimeOffsetSeconds     int64                     `json:"timeOffsetSeconds,omitempty"`
	StartOffsetSeconds    int64                     `json:"startOffsetSeconds,omitempty"`
	EndOffsetSeconds      int64                     `json:"endOffsetSeconds,omitempty"`
	StepSeconds           int64                     `json:"stepSeconds,omitempty"`
	TimeOffsets           []QueryInstantVariantSpec `json:"timeOffsets,omitempty"`
	RangeOffsets          []QueryRangeVariantSpec   `json:"rangeOffsets,omitempty"`
	RangeStepMatrix       bool                      `json:"rangeStepMatrix,omitempty"`
	Explain               bool                      `json:"explain,omitempty"`
	NativeLoweringMode    string                    `json:"nativeLoweringMode,omitempty"`
	ExpectedStatus        string                    `json:"expectedStatus,omitempty"`
	ExpectedErrorType     string                    `json:"expectedErrorType,omitempty"`
	ExpectedErrorContains string                    `json:"expectedErrorContains,omitempty"`
	// Subjects restricts this query row to a subset of configured subjects.
	// Supported names today: "shim", "promclick". Empty means compare against
	// all configured subjects.
	Subjects []string `json:"subjects,omitempty"`
	// DatasetVariants restricts this query row to a subset of seeded dataset
	// variants when a multi-dataset manifest is present. Supported names today:
	// "baseline", "resets_gaps", "churn_stale", "histogram_burst". Empty
	// means all seeded dataset variants.
	DatasetVariants []string `json:"datasetVariants,omitempty"`
	// ExcludeDatasetVariants removes specific seeded dataset variants from this
	// query row after DatasetVariants filtering. This allows broad corpora to
	// opt into all variants by default while excluding variants that are known
	// to be irrelevant or too noisy for one row.
	ExcludeDatasetVariants []string `json:"excludeDatasetVariants,omitempty"`
	// TargetPromP50MS optionally marks processing-benchmark rows that are
	// intended to put Prometheus in a useful wall-clock band on typical local
	// benchmark hardware. Classification is advisory and host-dependent.
	TargetPromP50MS *TargetPromP50MS `json:"targetPromP50Ms,omitempty"`
	// CompareMode selects how Prometheus and promshim responses are compared.
	// "exact" (default) requires byte-for-byte value equality. "structural" only
	// checks result type, series set, labels, timestamps, and NaN positions —
	// use it for queries (e.g. rate-family) where ClickHouse's computed values
	// legitimately diverge from Prometheus's extrapolated ones.
	CompareMode string `json:"compareMode,omitempty"`
}

type CompareCauseCluster struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Severity        string   `json:"severity,omitempty"`
	Bucket          string   `json:"bucket,omitempty"`
	Detail          string   `json:"detail,omitempty"`
	Count           int      `json:"count"`
	Variants        []string `json:"variants,omitempty"`
	Subjects        []string `json:"subjects,omitempty"`
	DatasetVariants []string `json:"datasetVariants,omitempty"`
}

type CompareReport struct {
	CorpusPath string                `json:"corpusPath"`
	Manifest   Manifest              `json:"manifest"`
	Results    []QueryComparison     `json:"results"`
	Clusters   []CompareCauseCluster `json:"clusters,omitempty"`
}

type QueryComparison struct {
	Name             string `json:"name"`
	Variant          string `json:"variant,omitempty"`
	DatasetVariant   string `json:"datasetVariant,omitempty"`
	CauseCluster     string `json:"causeCluster,omitempty"`
	CauseClusterSize int    `json:"causeClusterSize,omitempty"`
	// Subject identifies which subject-under-test this row compares against
	// the Prometheus oracle. Values today: "shim", "promclick". Each query
	// produces one row per configured subject so N-way compares stay flat.
	Subject     string `json:"subject"`
	Query       string `json:"query"`
	Status      string `json:"status"`
	Severity    string `json:"severity,omitempty"`
	Bucket      string `json:"bucket,omitempty"`
	CompareMode string `json:"compareMode,omitempty"`
	Detail      string `json:"detail,omitempty"`
}
