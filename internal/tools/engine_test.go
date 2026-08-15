package tools

import (
	"context"
	"fmt"
	"os"
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
	if err := os.WriteFile(full, []byte("export function greeting() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	doc := document.Document{Path: full, URI: "file://" + full, Hash: "test"}
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

func TestEngineOnboard(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	res, err := engine.Onboard(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Onboard failed: %v", err)
	}
	if res["config_path"] == "" {
		t.Fatal("expected config_path in result")
	}
	detected, ok := res["detected"].(map[string][]string)
	if !ok || len(detected["."]) == 0 {
		t.Fatalf("expected detected profiles for root, got %#v", res["detected"])
	}
}

func TestSearchSymbolsWithGopls(t *testing.T) {
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


