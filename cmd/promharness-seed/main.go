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
	log.Printf("seeded dataset: seed=%d base=%d step=%ds points=%d series=%d samples=%d", manifest.Seed, manifest.BaseUnixSeconds, manifest.StepSeconds, manifest.Points, manifest.SeriesCount, manifest.SampleCount)
}
