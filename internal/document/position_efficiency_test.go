package document

import (
	"bytes"
	"math/rand"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

func TestSourceLinePreservesSplitSemantics(t *testing.T) {
	inputs := [][]byte{
		nil, {}, []byte("\n"), []byte("\r\n"),
		[]byte("one"), []byte("one\n"), []byte("one\r\ntwo"),
		[]byte("one\n\ntwo\n"), []byte("日本語😀\r\ne\u0301\n"),
		bytes.Repeat([]byte("abc\n"), 10000),
	}
	for _, data := range inputs {
		lines := strings.Split(string(data), "\n")
		for line := -1; line <= len(lines)+2; line++ {
			got, ok := sourceLine(data, line)
			wantOK := line >= 0 && line < len(lines)
			if ok != wantOK {
				t.Fatalf("input length %d line %d: exists=%v, want %v", len(data), line, ok, wantOK)
			}
			if wantOK && got != strings.TrimSuffix(lines[line], "\r") {
				t.Fatalf("input length %d line %d: got %q, want %q", len(data), line, got, lines[line])
			}
		}
	}
}

func TestPositionConversionsMatchLegacyWholeFileSplit(t *testing.T) {
	rng := rand.New(rand.NewSource(20261102))
	segments := []string{"", "a", "foo", "日本語", "😀", "e\u0301", "\r", "xyz"}
	for trial := 0; trial < 120; trial++ {
		var b strings.Builder
		n := 1 + rng.Intn(80)
		for i := 0; i < n; i++ {
			for j := rng.Intn(8); j > 0; j-- {
				b.WriteString(segments[rng.Intn(len(segments))])
			}
			if i < n-1 {
				if rng.Intn(2) == 0 { b.WriteByte('\r') }
				b.WriteByte('\n')
			}
		}
		data := []byte(b.String())
		lines := strings.Split(string(data), "\n")
		for _, encoding := range []string{"utf-8", "utf-16", "utf-32"} {
			for i := -1; i <= len(lines); i++ {
				for _, col := range []int{-1, 0, 1, 2, 5, 50} {
					want, wantErr := legacyFromLSP(data, protocol.Position{Line: i, Character: col}, encoding)
					got, gotErr := FromLSP(data, protocol.Position{Line: i, Character: col}, encoding)
					if (wantErr == nil) != (gotErr == nil) || (wantErr == nil && got != want) {
						t.Fatalf("FromLSP trial %d encoding %s line %d col %d: got %+v %v, want %+v %v", trial, encoding, i, col, got, gotErr, want, wantErr)
					}
				}
				for _, col := range []int{0, 1, 2, 5, 50} {
					want, wantErr := legacyToLSP(data, core.Position{Line: i + 1, Column: col}, encoding)
					got, gotErr := ToLSP(data, core.Position{Line: i + 1, Column: col}, encoding)
					if (wantErr == nil) != (gotErr == nil) || (wantErr == nil && got != want) {
						t.Fatalf("ToLSP trial %d encoding %s line %d col %d: got %+v %v, want %+v %v", trial, encoding, i, col, got, gotErr, want, wantErr)
					}
				}
			}
		}
	}
}

func legacyFromLSP(data []byte, p protocol.Position, encoding string) (core.Position, error) {
	lines := strings.Split(string(data), "\n")
	if p.Line < 0 || p.Line >= len(lines) || p.Character < 0 {
		return core.Position{}, core.NewError(core.InvalidArgument, "position out of range")
	}
	cp, err := codePointAtUnits(strings.TrimSuffix(lines[p.Line], "\r"), p.Character, encoding)
	if err != nil { return core.Position{}, err }
	return core.Position{Line: p.Line + 1, Column: cp + 1}, nil
}

func legacyToLSP(data []byte, p core.Position, encoding string) (protocol.Position, error) {
	if p.Line < 1 || p.Column < 1 {
		return protocol.Position{}, core.NewError(core.InvalidArgument, "position must be one-based")
	}
	lines := strings.Split(string(data), "\n")
	if p.Line > len(lines) {
		return protocol.Position{}, core.NewError(core.InvalidArgument, "line out of range")
	}
	units, err := unitsAtCodePoint(strings.TrimSuffix(lines[p.Line-1], "\r"), p.Column-1, encoding)
	if err != nil { return protocol.Position{}, err }
	return protocol.Position{Line: p.Line - 1, Character: units}, nil
}

func BenchmarkLargeSourcePositionConversion(b *testing.B) {
	data := bytes.Repeat([]byte("const value = 42;\n"), 100000)
	for _, position := range []struct { name string; line int }{
		{"first", 0}, {"middle", 50000}, {"last", 99999},
	} {
		b.Run("new/"+position.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := FromLSP(data, protocol.Position{Line: position.line, Character: 6}, "utf-16"); err != nil { b.Fatal(err) }
			}
		})
		b.Run("legacy/"+position.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := legacyFromLSP(data, protocol.Position{Line: position.line, Character: 6}, "utf-16"); err != nil { b.Fatal(err) }
			}
		})
	}
}
