package tfplan

import "testing"

func TestShowRejectsFlagLikePlanFile(t *testing.T) {
	if _, err := Show(Request{PlanFile: "-help"}); err == nil {
		t.Error("esperava erro, obtive nil")
	}
}
