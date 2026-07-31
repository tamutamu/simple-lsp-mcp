package core

import "testing"

func TestTargetValidate(t *testing.T) {
	for _, tc := range []struct {
		target Target
		ok     bool
	}{
		{Target{SymbolID: "sym_a"}, true}, {Target{Path: "a.go", Line: 1, Column: 1}, true},
		{Target{}, false}, {Target{SymbolID: "sym_a", Path: "a.go", Line: 1, Column: 1}, false}, {Target{Path: "a.go", Line: 0, Column: 1}, false},
	} {
		if err := tc.target.Validate(); (err == nil) != tc.ok {
			t.Fatalf("Validate(%+v) = %v", tc.target, err)
		}
	}
}
