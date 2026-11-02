package tools

import (
	"fmt"
	"reflect"
	"testing"
)

// Reproduce a real project-warm-up failure, not just a microbenchmark:
// 513 unrelated CJK identifiers containing "id" previously exhausted the
// 512-file candidate cap before the LSP could answer an exact reference query.
func TestWarmUnicodeIdentifierNeighborsDoNotExhaustCandidateLimit(t *testing.T) {
	e, p, root := warmIndexFixture(t)
	const unrelated = projectWarmMaxMatches + 1
	for i := 0; i < unrelated; i++ {
		warmWrite(t, root, fmt.Sprintf("a-unrelated-%04d.ts", i),
			"export const 日本id語 = 1;\n")
	}
	warmWrite(t, root, "z-real.ts", "export const id = 1;\n")
	got, stats := warmScan(t, e, p, root, "id")
	if want := []string{"z-real.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CJK identifiers created false candidates: got=%v want=%v stats=%+v", got, want, stats)
	}
	if stats.Matches != 1 || stats.SourceFiles != unrelated+1 {
		t.Fatalf("unexpected project warm-up counts: %+v", stats)
	}
	again, cached := warmScan(t, e, p, root, "id")
	if !reflect.DeepEqual(again, got) || cached.FilesRead != 0 || cached.Matches != 1 {
		t.Fatalf("warm cached lookup changed result: got=%v stats=%+v", again, cached)
	}
}

func TestWarmUnicodePunctuationRemainsARealBoundary(t *testing.T) {
	for _, source := range []string{
		"const id\u00a0= 1;",
		"const id。 = 1;",
		"const 🚀id = 1;",
		"const id🚀 = 1;",
		"const id\u2028= 1;",
	} {
		if !warmContainsIdentifier([]byte(source), []byte("id")) {
			t.Fatalf("Unicode non-identifier boundary missed: %q", source)
		}
	}
}
