package tools

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

func TestIndexedLSPPositionMatchesLegacyForUnicodeAndCRLF(t *testing.T) {
	sources := []string{
		"", "a😀b\r\n日本語\n\nfoo", "a\n", "\r\n", "x\r",
		strings.Repeat("hello 日本語 😀\n", 100),
	}
	for _, source := range sources {
		text := []byte(source)
		idx := newEditPositionIndex(text)
		lines := strings.Split(source, "\n")
		for line, value := range lines {
			value = strings.TrimSuffix(value, "\r")
			for _, encoding := range []string{"utf-8", "utf-16", "utf-32"} {
				units, column, byteOffset := 0, 0, 0
				for {
					lsp := protocol.Position{Line: line, Character: units}
					got, off, err := idx.point(lsp, encoding)
					want, legacyErr := document.FromLSP(text, lsp, encoding)
					wantOff, offsetErr := byteOffsetAtCorePosition(text, want)
					if err != nil || legacyErr != nil || offsetErr != nil ||
						got != want || off != wantOff || off != idx.lineStarts[line]+byteOffset {
						t.Fatalf("line=%d column=%d encoding=%s: indexed=(%+v,%d,%v), legacy=(%+v,%d,%v,%v)",
							line, column, encoding, got, off, err, want, wantOff, legacyErr, offsetErr)
					}
					if byteOffset == len(value) {
						break
					}
					r, n := utf8.DecodeRuneInString(value[byteOffset:])
					step := 1
					if encoding == "utf-8" {
						step = n
					} else if encoding == "utf-16" && r > 0xffff {
						step = 2
					}
					if step > 1 {
						split := protocol.Position{Line: line, Character: units + 1}
						if _, _, err := idx.point(split, encoding); err == nil {
							t.Fatalf("split code point accepted at %+v (%s)", split, encoding)
						}
					}
					units += step
					column++
					byteOffset += n
				}
				invalid := protocol.Position{Line: line, Character: units + 1}
				if _, _, err := idx.point(invalid, encoding); err == nil {
					t.Fatalf("out-of-range position accepted: %+v", invalid)
				}
			}
		}
	}
}

func TestIndexedLSPRangeMatchesLegacy(t *testing.T) {
	text := []byte("const name = '😀';\r\nconst 日本語 = name;\n")
	idx := newEditPositionIndex(text)
	for _, encoding := range []string{"utf-8", "utf-16", "utf-32"} {
		for _, span := range []struct {
			start, end core.Position
		}{
			{core.Position{Line: 1, Column: 1}, core.Position{Line: 1, Column: 6}},
			{core.Position{Line: 1, Column: 15}, core.Position{Line: 1, Column: 16}},
			{core.Position{Line: 2, Column: 7}, core.Position{Line: 2, Column: 10}},
			{core.Position{Line: 2, Column: 14}, core.Position{Line: 3, Column: 1}},
		} {
			start, err := document.ToLSP(text, span.start, encoding)
			if err != nil { t.Fatal(err) }
			end, err := document.ToLSP(text, span.end, encoding)
			if err != nil { t.Fatal(err) }
			raw := protocol.Range{Start: start, End: end}
			got, a, z, err := idx.rangeAt(raw, encoding)
			if err != nil { t.Fatal(err) }
			want, err := rangeFromText(text, raw, encoding)
			if err != nil { t.Fatal(err) }
			wantA, err := byteOffsetAtCorePosition(text, want.Start)
			if err != nil { t.Fatal(err) }
			wantZ, err := byteOffsetAtCorePosition(text, want.End)
			if err != nil { t.Fatal(err) }
			if got != want || a != wantA || z != wantZ {
				t.Fatalf("encoding=%s indexed=(%+v,%d,%d), legacy=(%+v,%d,%d)",
					encoding, got, a, z, want, wantA, wantZ)
			}
		}
	}
}

func TestRequestLocalFileCacheReusesPositionIndex(t *testing.T) {
	cache := newFileCache()
	cache.bytes["source.ts"] = []byte("const id = 1;\n")
	cache.total = len(cache.bytes["source.ts"])
	first := cache.positionIndex("source.ts", cache.bytes["source.ts"])
	second := cache.positionIndex("source.ts", cache.bytes["source.ts"])
	if first != second || len(cache.positions) != 1 {
		t.Fatal("source line index was rebuilt despite request-local byte cache")
	}
	_ = cache.positionIndex("over-budget.ts", []byte("const id = 1;\n"))
	if len(cache.positions) != 1 {
		t.Fatal("uncached file unexpectedly retained a line index")
	}
}

func BenchmarkLSPPositionBatchConversion(b *testing.B) {
	text := []byte(strings.Repeat("const item = '日本語😀';\n", 2048))
	ranges := make([]protocol.Range, 256)
	for i := range ranges {
		line := i * 8
		ranges[i] = protocol.Range{
			Start: protocol.Position{Line: line, Character: 6},
			End: protocol.Position{Line: line, Character: 10},
		}
	}
	b.Run("previous-full-source-rescan", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			for _, r := range ranges {
				converted, err := rangeFromText(text, r, "utf-16")
				if err != nil { b.Fatal(err) }
				start, err := byteOffsetAtCorePosition(text, converted.Start)
				if err != nil { b.Fatal(err) }
				end, err := byteOffsetAtCorePosition(text, converted.End)
				if err != nil || start > end { b.Fatalf("invalid edit: %d..%d %v", start, end, err) }
			}
		}
	})
	b.Run("indexed-batch", func(b *testing.B) {
		b.ReportAllocs()
		for n := 0; n < b.N; n++ {
			idx := newEditPositionIndex(text)
			for _, r := range ranges {
				_, start, end, err := idx.rangeAt(r, "utf-16")
				if err != nil || start > end { b.Fatalf("invalid edit: %d..%d %v", start, end, err) }
			}
		}
	})
}
