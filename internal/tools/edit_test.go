package tools

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

func TestWorkspaceEditPlanPreviewAndApply(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "sample.go")
	original := "package p\nfunc Old() int { return Old() }\n"
	if err := os.WriteFile(full, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(full)}).String()
	edit := protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{
		uri: {
			{Range: protocol.Range{Start: protocol.Position{Line: 1, Character: 5}, End: protocol.Position{Line: 1, Character: 8}}, NewText: "New"},
			{Range: protocol.Range{Start: protocol.Position{Line: 1, Character: 24}, End: protocol.Position{Line: 1, Character: 27}}, NewText: "New"},
		},
	}}
	plan, preview, count, err := engine.workspaceEditPlan(edit, "utf-16")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || len(preview) != 1 || count != 2 || preview[0].Path != "sample.go" {
		t.Fatalf("plan=%#v preview=%#v count=%d", plan, preview, count)
	}
	if got, err := os.ReadFile(full); err != nil || string(got) != original {
		t.Fatalf("preview modified source: %q %v", got, err)
	}
	if _, err := engine.applyWorkspaceEditPlan(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "package p\nfunc New() int { return New() }\n" {
		t.Fatalf("applied content = %q", got)
	}
	info, err := os.Stat(full)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v err=%v", info.Mode(), err)
	}
}

func TestWorkspaceEditPlanRejectsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(outside)}).String()
	_, _, _, err = engine.workspaceEditPlan(protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{
		uri: {{Range: protocol.Range{}, NewText: "x"}},
	}}, "utf-16")
	if err == nil {
		t.Fatal("outside-workspace edit was accepted")
	}
}

func TestWorkspaceEditPlanRejectsResourceOperations(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	raw, err := json.Marshal(map[string]any{"kind": "rename", "oldUri": "file:///tmp/a", "newUri": "file:///tmp/b"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = engine.workspaceEditPlan(protocol.WorkspaceEdit{DocumentChanges: []json.RawMessage{raw}}, "utf-16")
	if err == nil || !strings.Contains(err.Error(), "resource operation") {
		t.Fatalf("resource operation error = %v", err)
	}
}

func TestWorkspaceEditPlanRejectsOverlappingEdits(t *testing.T) {
	root := t.TempDir()
	full := filepath.Join(root, "sample.go")
	if err := os.WriteFile(full, []byte("abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(full)}).String()
	_, _, _, err = engine.workspaceEditPlan(protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{
		uri: {
			{Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 1}, End: protocol.Position{Line: 0, Character: 4}}, NewText: "x"},
			{Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 3}, End: protocol.Position{Line: 0, Character: 5}}, NewText: "y"},
		},
	}}, "utf-16")
	if err == nil || !strings.Contains(err.Error(), "overlapping") {
		t.Fatalf("overlap error = %v", err)
	}
}

func TestApplyWorkspaceEditPlanRejectsStaleFileBeforeAnyWrite(t *testing.T) {
	root := t.TempDir()
	paths := []string{"a.go", "b.go"}
	for _, name := range paths {
		if err := os.WriteFile(filepath.Join(root, name), []byte("old\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	changes := map[string][]protocol.TextEdit{}
	for _, name := range paths {
		full := filepath.Join(root, name)
		uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(full)}).String()
		changes[uri] = []protocol.TextEdit{{Range: protocol.Range{Start: protocol.Position{Line: 0}, End: protocol.Position{Line: 0, Character: 3}}, NewText: "new"}}
	}
	plan, _, _, err := engine.workspaceEditPlan(protocol.WorkspaceEdit{Changes: changes}, "utf-16")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = engine.applyWorkspaceEditPlan(context.Background(), plan)
	var appErr *core.AppError
	if err == nil || !strings.Contains(err.Error(), string(core.StaleSymbol)) {
		_ = appErr
		t.Fatalf("stale apply error = %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if string(got) != "old\n" {
		t.Fatalf("first file was partially written: %q", got)
	}
}

func TestRenamePreparationHandlesRangeAndPlaceholder(t *testing.T) {
	text := []byte("func Old() {}\n")
	raw := json.RawMessage(`{"range":{"start":{"line":0,"character":5},"end":{"line":0,"character":8}},"placeholder":"Old"}`)
	result, err := renamePreparation(text, raw, "utf-16")
	if err != nil {
		t.Fatal(err)
	}
	if result["placeholder"] != "Old" {
		t.Fatalf("prepare = %#v", result)
	}
}
