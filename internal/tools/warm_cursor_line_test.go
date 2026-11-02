package tools

import (
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

// Preserve cursor spelling, CRLF and Unicode behavior while avoiding a
// whole-document allocation for each cross-file warm-up lookup.
func TestWarmIdentifierAtPositionLineParity(t *testing.T) {
	source := []byte("const first = 1;\r\nconst 日本語 = first;\nconst last = 日本語;\n")
	cases := []struct {
		name string
		pos  protocol.Position
		enc  string
		want string
	}{
		{"first", protocol.Position{Line: 0, Character: 7}, "utf-16", "first"},
		{"unicode-utf8", protocol.Position{Line: 1, Character: 6}, "utf-8", "日本語"},
		{"unicode-utf16", protocol.Position{Line: 1, Character: 6}, "utf-16", "日本語"},
		{"unicode-utf32", protocol.Position{Line: 1, Character: 6}, "utf-32", "日本語"},
		{"last", protocol.Position{Line: 2, Character: 7}, "utf-16", "last"},
		{"trailing-empty", protocol.Position{Line: 3, Character: 0}, "utf-16", ""},
		{"invalid-line", protocol.Position{Line: 4, Character: 0}, "utf-16", ""},
		{"invalid-column", protocol.Position{Line: 1, Character: 999}, "utf-16", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := warmIdentifierAtPosition(source, tc.pos, tc.enc); got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
			if got := warmIdentifierAtPositionLegacy(source, tc.pos, tc.enc); got != tc.want {
				t.Fatalf("legacy got %q; want %q", got, tc.want)
			}
		})
	}
}

// A test-only copy of the prior full-source split for controlled comparison.
func warmIdentifierAtPositionLegacy(text []byte, position protocol.Position, encoding string) string {
	point, err := document.FromLSP(text, position, encoding)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(text), "\n")
	if point.Line < 1 || point.Line > len(lines) {
		return ""
	}
	line := []rune(strings.TrimSuffix(lines[point.Line-1], "\r"))
	offset := point.Column - 1
	line, offset = warmDecodeLinePosition(line, offset)
	if offset == len(line) && offset > 0 {
		offset--
	}
	if offset >= 0 && offset < len(line) && !warmIdentRune(line[offset]) &&
		offset > 0 && warmIdentRune(line[offset-1]) {
		offset--
	}
	if offset < 0 || offset >= len(line) || !warmIdentRune(line[offset]) {
		return ""
	}
	start, end := offset, offset+1
	for start > 0 && warmIdentRune(line[start-1]) {
		start--
	}
	for end < len(line) && warmIdentRune(line[end]) {
		end++
	}
	return string(line[start:end])
}

var warmCursorBenchmarkResult string

func BenchmarkWarmIdentifierAtPositionLargeSource(b *testing.B) {
	const count = 100000
	source := []byte(strings.Repeat("const userId = 1;\n", count))
	for _, tc := range []struct {
		name string
		pos  protocol.Position
	}{
		{"first", protocol.Position{Line: 0, Character: 7}},
		{"last", protocol.Position{Line: count - 1, Character: 7}},
	} {
		for _, implementation := range []struct {
			name string
			run  func([]byte, protocol.Position, string) string
		}{
			{"old-full-split", warmIdentifierAtPositionLegacy},
			{"new-one-line", warmIdentifierAtPosition},
		} {
			b.Run(tc.name+"/"+implementation.name, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					warmCursorBenchmarkResult = implementation.run(source, tc.pos, "utf-16")
				}
				if warmCursorBenchmarkResult != "userId" {
					b.Fatalf("unexpected result: %q", warmCursorBenchmarkResult)
				}
			})
		}
	}
}
