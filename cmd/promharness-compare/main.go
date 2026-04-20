package main

import (
	"context"
	"log"

	"ch-observability/internal/promharness"
)

func main() {
	cfg, err := promharness.LoadCompareConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	report, err := promharness.RunCompare(ctx, cfg)
	for _, result := range report.Results {
		log.Printf("query=%s status=%s detail=%s", result.Name, result.Status, result.Detail)
	}
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("comparison complete: %d queries", len(report.Results))
}
