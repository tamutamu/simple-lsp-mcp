package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

func TestLocationPreviewsExactLineAndUTF8Bound(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("x", 235) + "😀tail"
	source := "package proof\nfunc Handle() {\n  useCall()\n  " + long + "\n}\n"
	if err := os.WriteFile(filepath.Join(root, "proof.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	e := New(ws, config.Runtime{})
	locations := []core.Location{
		{Path: "proof.go", Range: core.Range{Start: core.Position{Line: 3, Column: 3}}},
		{Path: "proof.go", Range: core.Range{Start: core.Position{Line: 4, Column: 3}}},
	}
	if err := e.populateLocationPreviews(locations); err != nil {
		t.Fatal(err)
	}
	if locations[0].Preview != "  useCall()" {
		t.Fatalf("unexpected preview: %q", locations[0].Preview)
	}
	if !strings.HasSuffix(locations[1].Preview, "…") || !strings.HasPrefix(locations[1].Preview, "  xxxx") {
		t.Fatalf("not safely abbreviated: %q", locations[1].Preview)
	}
}

func TestLocationPreviewsRejectMissingAndInvalidPath(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := New(ws, config.Runtime{})
	for _, item := range []core.Location{
		{Path: "../outside.go", Range: core.Range{Start: core.Position{Line: 1, Column: 1}}},
		{Path: "missing.go", Range: core.Range{Start: core.Position{Line: 1, Column: 1}}},
	} {
		if err := e.populateLocationPreviews([]core.Location{item}); err == nil {
			t.Fatalf("unexpected success for %q", item.Path)
		}
	}
}
