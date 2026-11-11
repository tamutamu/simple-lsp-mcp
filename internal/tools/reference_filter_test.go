package tools

import (
	"reflect"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestExcludeTestReferenceLocationsKeepsSemanticIdentityAndOrder(t *testing.T) {
	paths := []string{
		"cmd/app/main.go", "internal/tools/engine_test.go", "internal/tools/engine.go",
		"src/billing.test.ts", "src/lib.py", "backend/tests/test_account.py",
		"pkg/order.spec.tsx", "pkg/__tests__/order.ts", "src/lib_test.py",
	}
	refs := make([]core.Location, 0, len(paths))
	for _, p := range paths {
		refs = append(refs, core.Location{Path: p, SymbolID: p})
	}
	got := excludeTestReferenceLocations(refs)
	want := []string{"cmd/app/main.go", "internal/tools/engine.go", "src/lib.py"}
	if len(got) != len(want) {
		t.Fatalf("retained %d, want %d: %+v", len(got), len(want), got)
	}
	for i, loc := range got {
		if loc.Path != want[i] || loc.SymbolID != want[i] {
			t.Fatalf("semantic identity/order changed: %+v", got)
		}
	}
	if !reflect.DeepEqual(refs[1].Path, "internal/tools/engine_test.go") {
		t.Fatal("filter mutated original LSP result")
	}
}
