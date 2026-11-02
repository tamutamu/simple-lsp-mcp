package tools

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

// A flat documentSymbol response must never look exhaustive after a bad
// location was discarded. This previously returned the valid prefix as if
// the whole response had been converted.
func TestFlatDocumentSymbolsRejectsPartialLSPResponse(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(ws.Root(), "sample.go")
	text := []byte("package sample\nfunc Alpha() {}\n")
	if err := os.WriteFile(full, text, 0o600); err != nil {
		t.Fatal(err)
	}
	e := New(ws, config.Runtime{MaxResults: 10})
	uri := normalize.FileURI(full)
	doc := document.Document{Path: full, URI: uri, Text: text, Hash: "fixture"}
	profile := language.Profile{Name: "go", SessionKey: "go"}
	server := session.New("go", ws.Root(), config.Server{})
	valid := protocol.SymbolInformation{
		Name: "Alpha", Kind: 12,
		Location: protocol.Location{URI: uri, Range: protocol.Range{
			Start: protocol.Position{Line: 1, Character: 5},
			End: protocol.Position{Line: 1, Character: 10},
		}},
	}
	good, err := e.flatDocumentSymbols(profile, server, doc, []protocol.SymbolInformation{valid})
	if err != nil || len(good) != 1 || good[0].Name != "Alpha" {
		t.Fatalf("valid flat symbols: got=%+v err=%v", good, err)
	}
	cases := []struct {
		name string
		bad protocol.SymbolInformation
	}{
		{"invalid range", protocol.SymbolInformation{
			Name: "Stale", Kind: 12,
			Location: protocol.Location{URI: uri, Range: protocol.Range{
				Start: protocol.Position{Line: 1, Character: 999},
				End: protocol.Position{Line: 1, Character: 1000},
			}},
		}},
		{"outside workspace", protocol.SymbolInformation{
			Name: "Outside", Kind: 12,
			Location: protocol.Location{URI: normalize.FileURI(filepath.Join(t.TempDir(), "outside.go"))},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.flatDocumentSymbols(profile, server, doc, []protocol.SymbolInformation{valid, tc.bad})
			var appErr *core.AppError
			if !errors.As(err, &appErr) || appErr.Code != core.IncompleteSearch {
				t.Fatalf("partial outline accepted: got=%+v err=%v", got, err)
			}
			if len(got) != 0 {
				t.Fatalf("partial symbols leaked: %+v", got)
			}
		})
	}
}
