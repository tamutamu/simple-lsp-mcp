package tools

import (
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestExtractSymbolSourceLargeFileAndBoundaryCases(t *testing.T) {
	const prefixLines = 100000
	prefix := strings.Repeat("var unused = 1\n", prefixLines)
	data := []byte(prefix + "def target(value):\n    return value\n# after\n")
	tests := []struct {
		name       string
		start, end int
		endColumn  int
		maxLines   int
		maxBytes   int
		signature  bool
		want       string
		truncated  bool
		invalid    bool
	}{
		{"first lines", 1, 3, 1, 120, 16384, false, "var unused = 1\nvar unused = 1\n", false, false},
		{"middle line", 50000, 50000, 4, 120, 16384, false, "var unused = 1\n", false, false},
		{"last declaration", prefixLines + 1, prefixLines + 3, 2, 120, 16384, false, "def target(value):\n    return value\n# after\n", false, false},
		{"signature", prefixLines + 1, prefixLines + 3, 2, 120, 16384, true, "def target(value):\n", true, false},
		{"line limit", prefixLines + 1, prefixLines + 3, 2, 2, 16384, false, "def target(value):\n    return value\n", true, false},
		{"byte limit", prefixLines + 1, prefixLines + 3, 2, 120, 8, false, "def targ", true, false},
		{"trailing empty line", prefixLines + 4, prefixLines + 4, 2, 120, 16384, false, "", false, false},
		{"invalid end line", prefixLines + 1, prefixLines + 5, 2, 120, 16384, false, "", false, true},
		{"invalid start line", 0, 1, 2, 120, 16384, false, "", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := core.Range{
				Start: core.Position{Line: tc.start, Column: 1},
				End: core.Position{Line: tc.end, Column: tc.endColumn},
			}
			got, truncated, err := extractSymbolSource(data, r, tc.maxLines, tc.maxBytes, tc.signature)
			if tc.invalid {
				if err == nil {
					t.Fatalf("expected invalid range error, got %q", got)
				}
				return
			}
			if err != nil || got != tc.want || truncated != tc.truncated {
				t.Fatalf("got (%q, truncated=%v, err=%v); want (%q, truncated=%v)",
					got, truncated, err, tc.want, tc.truncated)
			}
		})
	}
}

func TestExtractSymbolSourceCRLFAndNoFinalNewline(t *testing.T) {
	data := []byte("before\r\nfunc target() {\r\n  return 1\r\n}")
	r := core.Range{Start: core.Position{Line: 2, Column: 1}, End: core.Position{Line: 4, Column: 2}}
	got, truncated, err := extractSymbolSource(data, r, 120, 16384, false)
	if err != nil || truncated || got != "func target() {\r\n  return 1\r\n}" {
		t.Fatalf("CRLF source: %q, truncated=%v, err=%v", got, truncated, err)
	}
	got, truncated, err = extractSymbolSource(data, r, 120, 16384, true)
	if err != nil || !truncated || got != "func target() {\r\n" {
		t.Fatalf("CRLF signature: %q, truncated=%v, err=%v", got, truncated, err)
	}
}

// This optimistic legacy baseline measures only full-file splitting plus
// joining two lines, without the additional legacy range/limit logic.
// It is therefore a lower bound for the old extraction cost.
var inspectSourceBenchmarkSink string

func BenchmarkInspectSourceLargeFile(b *testing.B) {
	const prefixLines = 100000
	data := []byte(strings.Repeat("var unused = 1\n", prefixLines) + "def target(value):\n    return value\n")
	for _, tc := range []struct {
		name  string
		start int
	}{
		{"first", 1},
		{"last", prefixLines + 1},
	} {
		b.Run(tc.name+"/legacy_full_split_lower_bound", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				lines := strings.SplitAfter(string(data), "\n")
				inspectSourceBenchmarkSink = strings.Join(lines[tc.start-1:tc.start+1], "")
			}
		})
		b.Run(tc.name+"/bounded_extract", func(b *testing.B) {
			b.ReportAllocs()
			r := core.Range{
				Start: core.Position{Line: tc.start, Column: 1},
				End: core.Position{Line: tc.start + 1, Column: 10},
			}
			for i := 0; i < b.N; i++ {
				s, _, err := extractSymbolSource(data, r, 120, 16384, false)
				if err != nil {
					b.Fatal(err)
				}
				inspectSourceBenchmarkSink = s
			}
		})
	}
}
