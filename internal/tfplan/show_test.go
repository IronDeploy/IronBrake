package tfplan

import "testing"

func TestShowRejectsFlagLikePlanFile(t *testing.T) {
	if _, err := Show("", "", "-help"); err == nil {
		t.Error("esperava erro, obtive nil")
	}
}
