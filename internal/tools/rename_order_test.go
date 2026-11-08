package tools

import (
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

// gopls may return the SAME semantic edits in different orders after it
// finishes loading packages. Comparing the raw WorkspaceEdit therefore
// caused false INCOMPLETE_SEARCH errors. Only normalized, validated edits
// are comparable.
func TestRenameSemanticallyEqualWorkspaceEditsIgnoreOrder(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sample.go")
	content := "package p\nfunc Old() { Old(); Old() }\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	engine := New(ws, config.Runtime{MaxResults: 20})
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	line := strings.Split(content, "\n")[1]
	var edits []protocol.TextEdit
	for offset := 0; offset < len(line); {
		next := strings.Index(line[offset:], "Old")
		if next < 0 {
			break
		}
		start := offset + next
		edits = append(edits, protocol.TextEdit{
			Range: protocol.Range{
				Start: protocol.Position{Line: 1, Character: start},
				End:   protocol.Position{Line: 1, Character: start + 3},
			},
			NewText: "New",
		})
		offset = start + 3
	}
	if len(edits) != 3 {
		t.Fatalf("test fixture has %d references", len(edits))
	}
	reordered := []protocol.TextEdit{edits[2], edits[0], edits[1]}
	first := protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{uri: edits}}
	second := protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{uri: reordered}}
	if reflect.DeepEqual(first, second) {
		t.Fatal("fixture must have different raw ordering")
	}
	_, a, na, err := engine.workspaceEditPlan(first, "utf-16")
	if err != nil {
		t.Fatal(err)
	}
	_, b, nb, err := engine.workspaceEditPlan(second, "utf-16")
	if err != nil {
		t.Fatal(err)
	}
	if na != 3 || nb != 3 || !reflect.DeepEqual(a, b) {
		t.Fatalf("equivalent semantic edits were not normalized: %v vs %v", a, b)
	}
}
