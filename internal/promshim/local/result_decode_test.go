package local

import (
	"math"
	"strings"
	"testing"
)

func TestRawPromValueToFloat64ParsesNumericSamples(t *testing.T) {
	value, err := rawPromValueToFloat64([]byte("123.5"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if value != 123.5 {
		t.Fatalf("expected 123.5, got %v", value)
	}
}

func TestRawPromValueToFloat64ParsesStringNaNSamples(t *testing.T) {
	value, err := rawPromValueToFloat64([]byte("\"NaN\""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !math.IsNaN(value) {
		t.Fatalf("expected NaN, got %v", value)
	}
}

func TestRawPromValueToFloat64RejectsNativeHistogramObjectSamples(t *testing.T) {
	_, err := rawPromValueToFloat64([]byte(`{"schema":0,"count":"10","sum":"20","buckets":[[1,"0","1"]]}`))
	if err == nil {
		t.Fatal("expected unsupported error for native histogram object")
	}
	if internalErrorKindOf(err) != internalErrorKindUnsupported {
		t.Fatalf("expected unsupported error kind, got %v (%T)", internalErrorKindOf(err), err)
	}
	if !strings.Contains(err.Error(), "native histogram values are not supported yet") {
		t.Fatalf("expected unsupported native histogram message, got: %v", err)
	}
}

func TestDecodeInstantSamplesRejectsNativeHistogramObjectAsUnsupported(t *testing.T) {
	payload := `{"tags":[["job","api"]],"timestamp":"2026-04-20 11:34:00.000","value":{"schema":0,"count":1,"sum":10}}`
	_, err := DecodeInstantSamples(strings.NewReader(payload))
	if err == nil {
		t.Fatal("expected unsupported error")
	}
	if internalErrorKindOf(err) != internalErrorKindUnsupported {
		t.Fatalf("expected unsupported error from decode, got kind=%v err=%v", internalErrorKindOf(err), err)
	}
}

func TestDecodeRangeSeriesRejectsNativeHistogramObjectAsUnsupported(t *testing.T) {
	payload := `{"tags":[["job","api"]],"time_series":[["2026-04-20 11:34:00.000",{"schema":0,"count":1,"sum":10}]]}`
	_, err := DecodeRangeSeries(strings.NewReader(payload))
	if err == nil {
		t.Fatal("expected unsupported error")
	}
	if internalErrorKindOf(err) != internalErrorKindUnsupported {
		t.Fatalf("expected unsupported error from decode, got kind=%v err=%v", internalErrorKindOf(err), err)
	}
}
