package tools

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

func TestWarmEscapedJSIdentifiersMatchExactSpellings(t *testing.T) {
	cases := []struct {
		source string
		want   bool
	}{
		{`const \u0069d = 1;`, true},
		{`const i\u0064 = 1;`, true},
		{`const \u{69}d = 1;`, true},
		{`const \u0069\u0064 = 1;`, true},
		{`const \u0069dentifier = 1;`, false},
		{`const \u0069d2 = 1;`, false},
		{`const grid = 1;`, false},
	}
	for _, tc := range cases {
		if got := warmContainsProjectIdentifier([]byte(tc.source), []byte("id"), "typescript-javascript"); got != tc.want {
			t.Errorf("source=%q: got %v, want %v", tc.source, got, tc.want)
		}
	}
	// Python does not permit JS-style Unicode escapes in identifier spelling.
	if warmContainsProjectIdentifier([]byte(`const \u0069d = 1;`), []byte("id"), "python") {
		t.Fatal("JavaScript escape matching must not affect Python warm-up")
	}
}

func TestWarmEscapedJSIdentifierAtCursor(t *testing.T) {
	src := []byte(`const \u0069d = 1;`)
	// Cursor positions within any of the six source characters of \u0069
	// and the final literal 'd' all denote the identifier "id".
	for column := 6; column <= 12; column++ {
		got := warmIdentifierAtPosition(src, protocol.Position{Line: 0, Character: column}, "utf-16")
		if got != "id" {
			t.Fatalf("cursor column %d: got %q, want id", column, got)
		}
	}
}

func TestWarmEscapedJSIdentifiersInProjectScan(t *testing.T) {
	e, p, root := warmIndexFixture(t)
	warmWrite(t, root, "plain.ts", "export const id = 1;\n")
	warmWrite(t, root, "escaped.ts", "export const "+`\u0069d`+" = 2;\n")
	warmWrite(t, root, "unrelated.ts", "export const "+`\u0069dentifier`+" = 3;\n")
	var matches []string
	scan := func(name string) warmScanStats {
		t.Helper()
		matches = nil
		stats, err := e.scanProjectWarmCandidates(context.Background(), p, root, []byte(name),
			func(_ string, rel string, _ language.Profile, _ []byte) error {
				matches = append(matches, rel)
				return nil
			})
		if err != nil {
			t.Fatal(err)
		}
		return stats
	}
	stats := scan("id")
	if want := []string{"escaped.ts", "plain.ts"}; !reflect.DeepEqual(matches, want) || stats.Matches != 2 {
		t.Fatalf("escaped reference omitted: matches=%v stats=%+v", matches, stats)
	}
	stats = scan("id")
	if len(matches) != 2 || stats.FilesRead != 0 {
		t.Fatalf("cached escaped reference was reread or omitted: matches=%v stats=%+v", matches, stats)
	}
}

func TestWarmEscapedJSIdentifierInUncachedBloomSource(t *testing.T) {
	e, p, root := warmIndexFixture(t)
	large := strings.Repeat("const unrelated = 1;\n", warmIndexMaxCachedBytes/len("const unrelated = 1;\n")+1)
	large += "export const " + `\u0069d` + " = 1;\n"
	warmWrite(t, root, "large.ts", large)
	scan := func(name string) warmScanStats {
		t.Helper()
		stats, err := e.scanProjectWarmCandidates(context.Background(), p, root, []byte(name),
			func(_ string, _ string, _ language.Profile, _ []byte) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		return stats
	}
	first := scan("id")
	if first.Matches != 1 || first.FilesRead != 1 {
		t.Fatalf("cold escaped match: %+v", first)
	}
	entry := e.warmIndex.snapshot(root+"\x00"+p.SessionKey)[filepath.Join(root, "large.ts")]
	if entry.content != nil || !warmIndexedMayContain(&entry, []byte("id")) {
		t.Fatal("Bloom index must retain the decoded escaped spelling")
	}
	miss := scan("noSuchIdentifier123")
	if miss.Matches != 0 || miss.FilesRead != 0 {
		t.Fatalf("decoded Bloom filter did not skip a negative lookup: %+v", miss)
	}
	hit := scan("id")
	if hit.Matches != 1 || hit.FilesRead != 1 {
		t.Fatalf("decoded Bloom filter excluded a valid reference: %+v", hit)
	}
}
