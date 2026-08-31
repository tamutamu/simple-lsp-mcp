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

func TestTargetValidateAcceptsSymbolPath(t *testing.T) {
	if err := (Target{SymbolPath: "UserService/createUser"}).Validate(); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
}

func TestTargetValidateAllowsPathAsScopeForSymbolPath(t *testing.T) {
	if err := (Target{SymbolPath: "createUser", Path: "src/user_service.ts"}).Validate(); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
}

func TestTargetValidateRejectsSymbolPathWithSymbolID(t *testing.T) {
	if err := (Target{SymbolPath: "createUser", SymbolID: "sym_a"}).Validate(); err == nil {
		t.Fatal("Validate = nil, want error")
	}
}

func TestTargetValidateRejectsSymbolIDWithPath(t *testing.T) {
	if err := (Target{SymbolID: "sym_a", Path: "src/a.go"}).Validate(); err == nil {
		t.Fatal("Validate = nil, want error")
	}
}

func TestTargetValidateRejectsSymbolPathWithPosition(t *testing.T) {
	if err := (Target{SymbolPath: "createUser", Line: 1, Column: 1}).Validate(); err == nil {
		t.Fatal("Validate = nil, want error")
	}
}
