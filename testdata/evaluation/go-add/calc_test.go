package calc

import "testing"

func TestAdd(t *testing.T) {
	for _, tc := range []struct{ a, b, want int }{{2, 3, 5}, {-4, 3, -1}, {0, 0, 0}} {
		if got := Add(tc.a, tc.b); got != tc.want {
			t.Fatalf("Add(%d, %d) = %d; want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
