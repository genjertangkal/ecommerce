package commerce

import "testing"

func TestSummarise(t *testing.T) {
	t.Parallel()

	got := Summarise("commerce", 12, 45600)
	if got.Team != "commerce" {
		t.Errorf("Team = %q, want %q", got.Team, "commerce")
	}
	if got.ProductCount != 12 {
		t.Errorf("ProductCount = %d, want 12", got.ProductCount)
	}
	if got.TotalValueCents != 45600 {
		t.Errorf("TotalValueCents = %d, want 45600", got.TotalValueCents)
	}
}
