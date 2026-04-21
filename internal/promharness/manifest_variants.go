package promharness

import "time"

func manifestVariants(manifest Manifest) []Manifest {
	if len(manifest.Variants) > 0 {
		out := make([]Manifest, len(manifest.Variants))
		copy(out, manifest.Variants)
		return out
	}
	return []Manifest{manifest}
}

func datasetVariantSeparation(cfg SeedConfig) time.Duration {
	points := cfg.Points
	if points < 2 {
		points = 2
	}
	return time.Duration(points*10) * cfg.Step
}

func manifestDatasetVariantName(manifest Manifest) string {
	if manifest.DatasetVariant != "" {
		return manifest.DatasetVariant
	}
	return "baseline"
}
