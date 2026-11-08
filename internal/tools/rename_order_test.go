package tools

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
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
func TestDominatingRenameSnapshotHandlesAlternatingSupersets(t *testing.T) {
	one := editPreview{Range: core.Range{Start: core.Position{Line: 2, Column: 3}, End: core.Position{Line: 2, Column: 7}}, NewText: "After"}
	two := editPreview{Range: core.Range{Start: core.Position{Line: 4, Column: 5}, End: core.Position{Line: 4, Column: 9}}, NewText: "After"}
	small := renameEditSnapshot{Files: []fileEditPreview{{Path: "app.go", Edits: []editPreview{one}}}, Count: 1}
	large := renameEditSnapshot{Files: []fileEditPreview{{Path: "app.go", Edits: []editPreview{one, two}}}, Count: 2}
	got, fluctuated, err := dominatingRenameSnapshot([]renameEditSnapshot{large, small, large, small, large})
	if err != nil || !fluctuated || !reflect.DeepEqual(got.Files, large.Files) {
		t.Fatalf("alternating subset incorrectly rejected: %#v, fluctuated=%v, err=%v", got, fluctuated, err)
	}
	same, fluctuated, err := dominatingRenameSnapshot([]renameEditSnapshot{large, large, large})
	if err != nil || fluctuated || !reflect.DeepEqual(same.Files, large.Files) {
		t.Fatalf("identical plans incorrectly flagged: %#v, fluctuated=%v, err=%v", same, fluctuated, err)
	}
	// Equal-size plans with conflicting edits cannot be merged or preferred.
	incompatible := renameEditSnapshot{Files: []fileEditPreview{{
		Path: "app.go", Edits: []editPreview{two},
	}}, Count: 1}
	if _, _, err := dominatingRenameSnapshot([]renameEditSnapshot{small, incompatible}); err == nil {
		t.Fatal("incomparable source edits must fail without writing")
	}
}
func TestRenameBatchSkipsOnlyExactPreviouslyEditedDeclarations(t *testing.T) {
	uri := "file:///workspace/service.go"
	point := protocol.Position{Line: 9, Character: 14}
	edit := protocol.TextEdit{Range: protocol.Range{
		Start: point, End: protocol.Position{Line: 9, Character: 28},
	}, NewText: "After"}
	plan := protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{uri: {edit}}}
	if !renameEditCoversTarget(plan, uri, point) {
		t.Fatal("declaration already in a semantic edit was not recognized")
	}
	if renameEditCoversTarget(plan, uri, protocol.Position{Line: 9, Character: 15}) ||
		renameEditCoversTarget(plan, "file:///workspace/other.go", point) {
		t.Fatal("distinct or offset declaration was silently skipped")
	}
	document := protocol.TextDocumentEdit{
		TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{URI: uri},
		Edits:        []protocol.TextEdit{edit},
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	fromDocumentChanges := protocol.WorkspaceEdit{DocumentChanges: []json.RawMessage{raw}}
	if !renameEditCoversTarget(fromDocumentChanges, uri, point) {
		t.Fatal("LSP documentChanges edit did not cover exact declaration")
	}
}
