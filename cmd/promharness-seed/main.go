package main

import (
	"context"
	"log"
	"time"

	"ch-observability/internal/promharness"
)

func main() {
	cfg, err := promharness.LoadSeedConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	manifest, err := promharness.RunSeed(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	if len(manifest.Variants) > 0 {
		for _, variant := range manifest.Variants {
			log.Printf("seeded dataset variant=%s seed=%d base=%d step=%ds points=%d series=%d samples=%d", variant.DatasetVariant, variant.Seed, variant.BaseUnixSeconds, variant.StepSeconds, variant.Points, variant.SeriesCount, variant.SampleCount)
		}
		return
	}
	log.Printf("seeded dataset: seed=%d base=%d step=%ds points=%d series=%d samples=%d", manifest.Seed, manifest.BaseUnixSeconds, manifest.StepSeconds, manifest.Points, manifest.SeriesCount, manifest.SampleCount)
}
