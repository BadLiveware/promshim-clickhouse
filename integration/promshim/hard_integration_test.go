package promshim_test

import "testing"

func TestHardVectorMatchingUnsupported(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%20*%20on(job)%20group_left%20target_info")
	if err != nil {
		t.Fatal(err)
	}
	assertUnsupportedContains(t, payload, "difficulty=hard", "vector matching")
}

func TestHardSetOperatorUnsupported(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%20and%20up")
	if err != nil {
		t.Fatal(err)
	}
	assertUnsupportedContains(t, payload, "difficulty=hard", "vector matching")
}

func TestHardSubqueryUnsupported(t *testing.T) {
	f := requireFixture(t)
	payload, err := f.getJSON("/api/v1/query?query=up%5B5m%3A30s%5D")
	if err != nil {
		t.Fatal(err)
	}
	assertUnsupportedContains(t, payload, "difficulty=hard", "subqueries")
}
