package main

import (
	"context"
	"log"

	"github.com/BadLiveware/promshim-ch/internal/promharness"
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
		if result.Variant != "" || result.DatasetVariant != "" {
			log.Printf("query=%s dataset_variant=%s variant=%s status=%s detail=%s", result.Name, result.DatasetVariant, result.Variant, result.Status, result.Detail)
			continue
		}
		log.Printf("query=%s status=%s detail=%s", result.Name, result.Status, result.Detail)
	}
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("comparison complete: %d queries", len(report.Results))
}
