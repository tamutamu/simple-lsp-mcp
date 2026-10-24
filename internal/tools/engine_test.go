package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

func TestSearchSymbolsRequiresLanguage(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{Servers: map[string][]config.Server{}, MaxResults: 10})
	if _, err := engine.SearchSymbols(context.Background(), map[string]any{}); err == nil {
		t.Fatal("SearchSymbols without language returned nil error")
	}
}

func TestSearchSymbolsRequiresNonEmptyQuery(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{Servers: map[string][]config.Server{}, MaxResults: 10})

	for name, input := range map[string]map[string]any{
		"missing query": {"language": "go"},
		"empty query":   {"language": "go", "query": ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err = engine.SearchSymbols(context.Background(), input)
			if err == nil {
				t.Fatal("SearchSymbols without a non-empty query returned nil error")
			}
			var appErr *core.AppError
			if !errors.As(err, &appErr) {
				t.Fatalf("error = %T %v, want *core.AppError", err, err)
			}
			if appErr.Code != core.InvalidArgument {
				t.Fatalf("error code = %q, want %q", appErr.Code, core.InvalidArgument)
			}
			if appErr.Message != "query must be a non-empty string" {
				t.Fatalf("error message = %q, want non-empty query message", appErr.Message)
			}
		})
	}
}

func TestLocationsDecodesLSPLocationsBeforeLocationLinks(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Root(), "example.go"), []byte("package test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	raw := []byte(fmt.Sprintf(`[{"uri":"file://%s/example.go","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":7}}}]`, ws.Root()))
	locations, err := engine.locations(raw, "utf-16")
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 1 || locations[0].Path != "example.go" {
		t.Fatalf("locations = %#v", locations)
	}
}

func TestDocumentTreeConvertsDocumentAbsolutePathToWorkspaceRelativePath(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(ws.Root(), "greeting.ts")
	text := []byte("export function greeting() {}\n")
	if err := os.WriteFile(full, text, 0o600); err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	doc := document.Document{Path: full, URI: "file://" + full, Hash: "test", Text: text}
	symbols := engine.documentTree(language.Profile{Name: "typescript", SessionKey: "typescript-javascript"}, session.New("typescript-javascript", ws.Root(), config.Server{}), doc, []protocol.DocumentSymbol{{
		Name:           "greeting",
		Kind:           12,
		Range:          protocol.Range{Start: protocol.Position{}, End: protocol.Position{Line: 0, Character: 29}},
		SelectionRange: protocol.Range{Start: protocol.Position{Character: 16}, End: protocol.Position{Character: 24}},
	}})
	if len(symbols) != 1 {
		t.Fatalf("symbols = %#v, want one symbol", symbols)
	}
	if got := symbols[0].(map[string]any)["path"]; got != "greeting.ts" {
		t.Fatalf("path = %#v, want greeting.ts", got)
	}
}

func TestDocumentTreeStillEmitsLegacyKeys(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(ws.Root(), "greeting.ts")
	text := []byte("export function greeting() {}\n")
	if err := os.WriteFile(full, text, 0o600); err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	doc := document.Document{Path: full, URI: "file://" + full, Hash: "test", Text: text}
	symbols := engine.documentTree(language.Profile{Name: "typescript", SessionKey: "typescript-javascript"}, session.New("typescript-javascript", ws.Root(), config.Server{}), doc, []protocol.DocumentSymbol{{
		Name: "greeting",
		Kind: 12,
	}})
	if len(symbols) != 1 {
		t.Fatalf("symbols = %#v, want one symbol", symbols)
	}
	m := symbols[0].(map[string]any)
	for _, key := range []string{"symbol_id", "name", "kind", "language", "path", "range", "selection_range"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("legacy key %q missing from %#v", key, m)
		}
	}
}

func TestRangeFromTextMatchesRangeForPath(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	text := []byte("line one\nline two\nline three\n")
	full := filepath.Join(ws.Root(), "f.txt")
	if err := os.WriteFile(full, text, 0o600); err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	r := protocol.Range{Start: protocol.Position{Line: 1, Character: 2}, End: protocol.Position{Line: 2, Character: 4}}
	fromPath, err := engine.rangeForPath("f.txt", r, "utf-16")
	if err != nil {
		t.Fatal(err)
	}
	fromText, err := rangeFromText(text, r, "utf-16")
	if err != nil {
		t.Fatal(err)
	}
	if fromPath != fromText {
		t.Fatalf("rangeForPath = %#v, rangeFromText = %#v", fromPath, fromText)
	}
}

func TestResolveSymbolPathWithGopls(t *testing.T) {
	requireGopls(t)
	ws, err := workspace.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Runtime{Workspace: ws.Root(), RequestTimeout: 10 * time.Second, MaxResults: 10})
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, cfg)
	defer engine.Sessions.Shutdown(context.Background())

	p, err := language.Require("go")
	if err != nil {
		t.Fatal(err)
	}
	hits, warnings, err := engine.resolveSymbolPath(context.Background(), p, "New", "engine.go")
	if err != nil {
		t.Fatalf("resolveSymbolPath failed: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %#v, warnings = %v", hits, warnings)
	}
	if hits[0].Node.SymbolPath != "New" {
		t.Fatalf("SymbolPath = %q, want New", hits[0].Node.SymbolPath)
	}
}

func TestSearchSymbolsWithGopls(t *testing.T) {
	requireGopls(t)
	ws, err := workspace.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Runtime{Workspace: ws.Root(), RequestTimeout: 10 * time.Second, MaxResults: 10})
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, cfg)
	defer engine.Sessions.Shutdown(context.Background())

	res, err := engine.SearchSymbols(context.Background(), map[string]any{
		"language": "go",
		"query":    "New",
	})
	if err != nil {
		t.Fatalf("SearchSymbols failed: %v", err)
	}
	symbols, ok := res["symbols"].([]core.SymbolSummary)
	if !ok || len(symbols) == 0 {
		t.Fatalf("expected symbols, got %#v", res)
	}
	t.Logf("found %d symbols with query 'New'", len(symbols))
}

func TestSearchSymbolsSubdirectoryProfile(t *testing.T) {
	requireGopls(t)
	tempDir := t.TempDir()
	apiDir := filepath.Join(tempDir, "apps", "api")
	if err := os.MkdirAll(apiDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apiDir, "go.mod"), []byte("module api\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apiDir, "server.go"), []byte("package main\n\ntype ServerConfig struct{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	configContent := `
apps/api:
  go:
    command: gopls
    args: []
`
	if err := os.WriteFile(filepath.Join(tempDir, config.ConfigFile), []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	ws, err := workspace.Open(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Runtime{Workspace: ws.Root(), RequestTimeout: 10 * time.Second, MaxResults: 10})
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, cfg)
	defer engine.Sessions.Shutdown(context.Background())

	res, err := engine.SearchSymbols(context.Background(), map[string]any{
		"language": "go",
		"query":    "ServerConfig",
	})
	if err != nil {
		t.Fatalf("SearchSymbols in subdirectory failed: %v", err)
	}
	symbols, ok := res["symbols"].([]core.SymbolSummary)
	if !ok || len(symbols) == 0 {
		t.Fatalf("expected symbols for ServerConfig, got %#v", res)
	}
	t.Logf("found %d symbols in subdirectory profile", len(symbols))
}

func requireGopls(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("real gopls not on PATH")
	}
}