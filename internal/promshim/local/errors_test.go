package local

import (
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

func TestNormalizeInternalErrorMapsNativeDuplicateJoinGuardToBadData(t *testing.T) {
	err := NormalizeInternalError(&storage.QueryError{
		ErrorType: "execution",
		Message:   "Code: 395. DB::Exception: found duplicate series for the match group on the lhs hand-side of the operation",
	})
	if !IsBadDataError(err) {
		t.Fatalf("expected bad_data classification, got %#v", err)
	}
	if got := userFacingErrorMessage(err); got != "multiple matches for labels: many-to-one matching must be explicit (group_left/group_right)" {
		t.Fatalf("unexpected normalized message: %q", got)
	}
}
