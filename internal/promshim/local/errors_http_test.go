package local

import (
	"testing"

	"ch-observability/internal/promshim/model"
	"ch-observability/internal/promshim/plan"
)

func TestAPIErrorFromInternalStripsContextFromBadDataError(t *testing.T) {
	err := WithInternalContext(WithInternalContext(NewBadDataErrorf("%s", model.ErrDuplicateLabelsetTimestamps.Error()), "applying binary expression op=* returnBool=false"), "evaluating query plan in instant mode")
	apiErr := apiErrorFromInternal(err)
	if apiErr.ErrorType != "bad_data" {
		t.Fatalf("expected bad_data error type, got %#v", apiErr)
	}
	if apiErr.Error != model.ErrDuplicateLabelsetTimestamps.Error() {
		t.Fatalf("expected duplicate labelset message, got %q", apiErr.Error)
	}
}

func TestAPIErrorFromInternalUsesUserFacingPlanBuildErrorMessage(t *testing.T) {
	expr, err := plan.ParseExpression(`rate(up[5m:30s])`)
	if err != nil {
		t.Fatal(err)
	}
	apiErr := apiErrorFromInternal(NewPlanBuildError(expr, plan.SupportResult{Supported: false, Difficulty: plan.DifficultyHard, Reason: `function "rate" with subquery arguments is not implemented yet`}, "call planning"))
	if apiErr.ErrorType != "unsupported" {
		t.Fatalf("expected unsupported error type, got %#v", apiErr)
	}
	if apiErr.Error != `unsupported PromQL (difficulty=hard): function "rate" with subquery arguments is not implemented yet` {
		t.Fatalf("unexpected user-facing unsupported message: %q", apiErr.Error)
	}
}
