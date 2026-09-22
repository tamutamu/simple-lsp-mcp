package calc

import "testing"

func TestHiddenAddOracle(t *testing.T) {
	if got := Add(100, 17); got != 117 {
		t.Fatalf("Add(100,17) = %d, want 117", got)
	}
}
