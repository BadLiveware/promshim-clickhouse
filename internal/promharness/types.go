package promharness

import "time"

type SeedConfig struct {
	PromRemoteWriteURL       string
	ClickHouseRemoteWriteURL string
	Seed                     int64
	Step                     time.Duration
	Points                   int
	BaseTime                 time.Time
	ArtifactDir              string
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
	CorpusPath        string
	ArtifactDir       string
	Timeout           time.Duration
}

type QuerySpec struct {
	Name               string `json:"name"`
	Endpoint           string `json:"endpoint"`
	Query              string `json:"query"`
	TimeOffsetSeconds  int64  `json:"timeOffsetSeconds,omitempty"`
	StartOffsetSeconds int64  `json:"startOffsetSeconds,omitempty"`
	EndOffsetSeconds   int64  `json:"endOffsetSeconds,omitempty"`
	StepSeconds        int64  `json:"stepSeconds,omitempty"`
}

type CompareReport struct {
	CorpusPath string            `json:"corpusPath"`
	Manifest   Manifest          `json:"manifest"`
	Results    []QueryComparison `json:"results"`
}

type QueryComparison struct {
	Name   string `json:"name"`
	Query  string `json:"query"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}
