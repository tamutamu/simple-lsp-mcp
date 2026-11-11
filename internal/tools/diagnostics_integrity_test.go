package tools

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

func TestDiagnosticBatchRejectsInvalidPrimaryRangeWithoutPartialSuccess(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Root(), "sample.go"),
		[]byte("package sample\nvar x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	valid := protocol.Range{
		Start: protocol.Position{Line: 1, Character: 4},
		End:   protocol.Position{Line: 1, Character: 5},
	}
	invalid := protocol.Range{
		Start: protocol.Position{Line: 1, Character: 999},
		End:   protocol.Position{Line: 1, Character: 1000},
	}
	good := protocol.Diagnostic{Range: valid, Message: "real error"}
	bad := protocol.Diagnostic{Range: invalid, Message: "unconvertible error"}

	got, err := engine.diagnostics("sample.go", []protocol.Diagnostic{good, good}, "utf-16")
	if err != nil || len(got) != 2 {
		t.Fatalf("valid diagnostics = %+v, err = %v; want both entries", got, err)
	}

	for _, diagnostics := range [][]protocol.Diagnostic{{bad}, {good, bad}} {
		got, err = engine.diagnostics("sample.go", diagnostics, "utf-16")
		var appErr *core.AppError
		if !errors.As(err, &appErr) || appErr.Code != core.IncompleteSearch {
			t.Fatalf("invalid primary diagnostic silently dropped: got=%+v err=%v", got, err)
		}
		if len(got) != 0 {
			t.Fatalf("partial diagnostic results leaked: %+v", got)
		}
	}
}

// Compare one batch of diagnostics with the previous implementation, which
// reread the entire file for every range. This is a microbenchmark, not
// evidence of coding-agent A/B productivity.
func diagnosticBenchmarkFixture(b *testing.B) (*Engine, []protocol.Diagnostic) {
	b.Helper()
	ws, err := workspace.Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	source := bytes.Repeat([]byte("0123456789abcdef\n"), 1<<16)
	if err := os.WriteFile(filepath.Join(ws.Root(), "sample.go"), source, 0o600); err != nil {
		b.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 256})
	r := protocol.Range{
		Start: protocol.Position{Line: 0, Character: 0},
		End:   protocol.Position{Line: 0, Character: 1},
	}
	diagnostics := make([]protocol.Diagnostic, 128)
	for i := range diagnostics {
		diagnostics[i] = protocol.Diagnostic{Range: r, Message: "example"}
	}
	return engine, diagnostics
}

func BenchmarkDiagnosticsBatchLegacyRangeReads(b *testing.B) {
	engine, diagnostics := diagnosticBenchmarkFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for _, d := range diagnostics {
			if _, err := engine.rangeForPath("sample.go", d.Range, "utf-16"); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkDiagnosticsBatchCachedRanges(b *testing.B) {
	engine, diagnostics := diagnosticBenchmarkFixture(b)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		got, err := engine.diagnostics("sample.go", diagnostics, "utf-16")
		if err != nil || len(got) != len(diagnostics) {
			b.Fatalf("diagnostics=%d err=%v", len(got), err)
		}
	}
}

func TestUnscopedDiagnosticsExplicitlyWarnIncomplete(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	got, err := engine.Diagnostics(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	meta, ok := got["meta"].(core.Meta)
	if !ok || meta.Complete || len(meta.Warnings) == 0 || !strings.Contains(meta.Warnings[0], "specify path") {
		t.Fatalf("unscoped diagnostics must not appear clean: %#v", got)
	}
}
