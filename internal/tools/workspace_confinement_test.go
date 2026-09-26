package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

func TestFallbackDiscoveryRejectsSessionRootOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	profile, err := language.ForLanguage("go")
	if err != nil {
		t.Fatal(err)
	}
	s := session.New(profile.SessionKey, outside, config.Server{})
	if _, err := engine.sessionCandidateFiles(context.Background(), profile, s); err == nil {
		t.Fatal("pathless fallback accepted an LSP root outside the workspace")
	}
}

func TestRenameWarmupRejectsSessionRootOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	profile, err := language.ForLanguage("typescript")
	if err != nil {
		t.Fatal(err)
	}
	s := session.New(profile.SessionKey, outside, config.Server{})
	if err := engine.warmProjectSymbolCandidates(context.Background(), profile, s, "OldName"); err == nil {
		t.Fatal("project warm-up accepted an LSP root outside the workspace")
	}
}

func TestFallbackDiscoveryRejectsSymlinkedSessionRootOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "linked-root")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	profile, err := language.ForLanguage("go")
	if err != nil {
		t.Fatal(err)
	}
	s := session.New(profile.SessionKey, link, config.Server{})
	if _, err := engine.sessionCandidateFiles(context.Background(), profile, s); err == nil {
		t.Fatal("pathless fallback followed a symlinked LSP root outside the workspace")
	}
}

func TestRenameWarmupRejectsSymlinkedSessionRootOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "linked-root")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 10})
	profile, err := language.ForLanguage("typescript")
	if err != nil {
		t.Fatal(err)
	}
	s := session.New(profile.SessionKey, link, config.Server{})
	if err := engine.warmProjectSymbolCandidates(context.Background(), profile, s, "OldName"); err == nil {
		t.Fatal("rename warm-up followed a symlinked LSP root outside the workspace")
	}
}
