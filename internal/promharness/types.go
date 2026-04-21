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
}

type Manifest struct {
	Seed            int64  `json:"seed"`
	BaseUnixSeconds int64  `json:"baseUnixSeconds"`
	StepSeconds     int64  `json:"stepSeconds"`
	Points          int    `json:"points"`
	SeriesCount     int    `json:"seriesCount"`
	SampleCount     int    `json:"sampleCount"`
	GeneratedAtUTC  string `json:"generatedAtUtc"`
}

type CompareConfig struct {
	PrometheusBaseURL string
	PromshimBaseURL   string
	// PromClickBaseURL is optional. When empty, PromClick is not queried and
	// the compare pass runs as a 2-way comparison (Prometheus vs shim) as
	// before.
	PromClickBaseURL string
	CorpusPath       string
	ArtifactDir      string
	Timeout          time.Duration
}

type QuerySpec struct {
	Name                  string `json:"name"`
	Endpoint              string `json:"endpoint"`
	Query                 string `json:"query"`
	TimeOffsetSeconds     int64  `json:"timeOffsetSeconds,omitempty"`
	StartOffsetSeconds    int64  `json:"startOffsetSeconds,omitempty"`
	EndOffsetSeconds      int64  `json:"endOffsetSeconds,omitempty"`
	StepSeconds           int64  `json:"stepSeconds,omitempty"`
	ExpectedStatus        string `json:"expectedStatus,omitempty"`
	ExpectedErrorType     string `json:"expectedErrorType,omitempty"`
	ExpectedErrorContains string `json:"expectedErrorContains,omitempty"`
	// CompareMode selects how Prometheus and promshim responses are compared.
	// "exact" (default) requires byte-for-byte value equality. "structural" only
	// checks result type, series set, labels, timestamps, and NaN positions —
	// use it for queries (e.g. rate-family) where ClickHouse's computed values
	// legitimately diverge from Prometheus's extrapolated ones.
	CompareMode string `json:"compareMode,omitempty"`
}

type CompareReport struct {
	CorpusPath string            `json:"corpusPath"`
	Manifest   Manifest          `json:"manifest"`
	Results    []QueryComparison `json:"results"`
}

type QueryComparison struct {
	Name string `json:"name"`
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
