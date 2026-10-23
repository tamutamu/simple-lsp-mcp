package changeguarddemo

import "testing"

func TestCompleteOrder(t *testing.T) {
	if got := CompleteOrder("42"); got != "completed:42" {
		t.Fatalf("CompleteOrder() = %q", got)
	}
}
