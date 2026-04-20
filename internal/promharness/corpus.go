package promharness

import (
	"encoding/json"
	"os"
)

func LoadQueryCorpus(path string) ([]QuerySpec, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var queries []QuerySpec
	if err := json.Unmarshal(payload, &queries); err != nil {
		return nil, err
	}
	return queries, nil
}
