package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

func listTestEngine(t *testing.T) *Engine {
	t.Helper()
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(ws, config.Runtime{MaxResults: 500, Servers: map[string][]config.Server{
		"go":                    {{Command: "gopls", Directory: "."}},
		"typescript-javascript": {{Command: "typescript-language-server", Directory: "."}},
	}})
}

func writeListTestFile(t *testing.T, root, rel string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package example\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceListFilesScansAllConfiguredLanguagesButSkipsDependencies(t *testing.T) {
	e := listTestEngine(t)
	for _, path := range []string{
		"apps/a/z.go", "apps/a/a.go", "apps/b/c.ts", "notes.md",
		"node_modules/dependency.go", ".git/internal.go", "dist/output.ts",
	} {
		writeListTestFile(t, e.WS.Root(), path)
	}
	if err := os.Symlink(filepath.Join(e.WS.Root(), "apps/a/a.go"), filepath.Join(e.WS.Root(), "symlink.go")); err != nil {
		t.Logf("symlink unavailable on this platform: %v", err)
	}
	files, err := e.workspaceListFiles(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"apps/a/a.go", "apps/a/z.go", "apps/b/c.ts"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("files=%v want %v", files, want)
	}
	files, err = e.workspaceListFiles(context.Background(), "go")
	if err != nil || !reflect.DeepEqual(files, []string{"apps/a/a.go", "apps/a/z.go"}) {
		t.Fatalf("go files=%v error=%v", files, err)
	}
}

func TestWorkspaceListNestedSymbolsAndKindFilter(t *testing.T) {
	nodes := []symbolNode{{Name: "A", Kind: "class", SymbolPath: "A", Children: []symbolNode{
		{Name: "B", Kind: "method", SymbolPath: "A/B"},
		{Name: "C", Kind: "method", SymbolPath: "A/C"},
	}}}
	all := flattenWorkspaceNodes(nodes, map[string]any{})
	if len(all) != 3 || all[1].SymbolPath != "A/B" || all[2].SymbolPath != "A/C" {
		t.Fatalf("flat nodes=%#v", all)
	}
	filtered := flattenWorkspaceNodes(nodes, map[string]any{"kinds": []any{"method"}})
	if len(filtered) != 2 || filtered[0].Name != "B" || filtered[1].Name != "C" {
		t.Fatalf("filtered nodes=%#v", filtered)
	}
}

func TestWorkspaceListRejectsQueryUnsupportedLanguageAndInvalidCursor(t *testing.T) {
	e := listTestEngine(t)
	writeListTestFile(t, e.WS.Root(), "example.go")
	for _, args := range []map[string]any{
		{"query": "Foo"}, {"language": "python"}, {"cursor": "not-base64"},
		{"kinds": "function"}, {"cursor": 123}, {"limit": -1},
	} {
		_, err := e.ListWorkspaceSymbols(context.Background(), args)
		if err == nil {
			t.Fatalf("accepted invalid args: %#v", args)
		}
	}
}

func TestWorkspaceListWithoutConfiguredServersReturnsError(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 100})
	_, err = engine.ListWorkspaceSymbols(context.Background(), nil)
	var appErr *core.AppError
	if !errors.As(err, &appErr) || appErr.Code != core.UnsupportedLanguage {
		t.Fatalf("unconfigured listing returned %v", err)
	}
}

func TestWorkspaceListEmptyWorkspaceAndStaleFileList(t *testing.T) {
	e := listTestEngine(t)
	result, err := e.ListWorkspaceSymbols(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result["next_cursor"]; ok || !result["meta"].(core.Meta).Complete {
		t.Fatalf("empty workspace page=%#v", result)
	}
	writeListTestFile(t, e.WS.Root(), "a.go")
	page := workspaceListPage([]core.SymbolSummary{{Name: "A"}}, workspaceListCursor{Scope: "old", File: 0, Index: 1, Hash: "old"}, 1)
	_, err = e.ListWorkspaceSymbols(context.Background(), map[string]any{"cursor": page["next_cursor"]})
	var appErr *core.AppError
	if !errors.As(err, &appErr) || appErr.Code != core.InvalidArgument {
		t.Fatalf("stale cursor returned %v", err)
	}
}

func TestWorkspaceListPageCursorAdvancesAcrossFileAndSymbolOffsets(t *testing.T) {
	page := workspaceListPage([]core.SymbolSummary{{Name: "A"}}, workspaceListCursor{Scope: "scope", File: 4, Index: 3, Hash: "hash"}, 7)
	if meta := page["meta"].(core.Meta); meta.Complete || !meta.Truncated {
		t.Fatalf("incorrect pagination metadata: %#v", meta)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(page["next_cursor"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var cursor workspaceListCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil || cursor.File != 4 || cursor.Index != 3 || cursor.Hash != "hash" {
		t.Fatalf("cursor=%#v err=%v", cursor, err)
	}
	last := workspaceListPage(nil, workspaceListCursor{Scope: "scope", File: 7}, 7)
	if !last["meta"].(core.Meta).Complete {
		t.Fatalf("last page incorrectly incomplete: %#v", last)
	}
	if _, ok := last["next_cursor"]; ok {
		t.Fatalf("last page unexpectedly has cursor: %#v", last)
	}
}

func TestWorkspaceListReturnsIncompleteOnLSPFailure(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeListTestFile(t, ws.Root(), "file.go")
	e := New(ws, config.Runtime{MaxResults: 100, Servers: map[string][]config.Server{
		"go": {{Command: "nonexistent-lsp-server-for-test", Directory: "."}},
	}})
	_, err = e.ListWorkspaceSymbols(context.Background(), nil)
	var appErr *core.AppError
	if !errors.As(err, &appErr) || appErr.Code != core.IncompleteSearch {
		t.Fatalf("LSP failure returned %v instead of INCOMPLETE_SEARCH", err)
	}
}

func TestWorkspaceListScanStopsOnCancellation(t *testing.T) {
	e := listTestEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := e.workspaceListFiles(ctx, "")
	if err == nil {
		t.Fatal("cancelled listing unexpectedly succeeded")
	}
}

func TestWorkspaceListCanFlattenDocumentSymbolShapes(t *testing.T) {
	nodes := walkDocument([]byte("type A struct{}\n"), "utf-16", []protocol.DocumentSymbol{
		{Name: "A", Kind: 23, Children: []protocol.DocumentSymbol{{Name: "M", Kind: 6}}},
	}, nil, "")
	if flat := flattenWorkspaceNodes(nodes, nil); len(flat) != 2 || flat[1].SymbolPath != "A/M" {
		t.Fatalf("flattened nested symbols=%#v", flat)
	}
}
