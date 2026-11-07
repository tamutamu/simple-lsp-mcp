package tools

import (
	"reflect"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestCompactAppliedRenameRetainsFactsButNotDuplicateEdits(t *testing.T) {
	full := map[string]any{
		"applied": true, "new_name": "newName", "file_count": 2, "edit_count": 3,
		"files": []fileEditPreview{
			{Path: "app/a.ts", Edits: []editPreview{{Range: core.Range{}, NewText: "newName"}}},
			{Path: "app/b.ts", Edits: []editPreview{{}, {}}},
		},
		"warnings": []string{"a warning"},
	}
	out := CompactAppliedRename(full)
	if !reflect.DeepEqual(out["changed_files"], []string{"app/a.ts", "app/b.ts"}) ||
		out["applied"] != true || out["file_count"] != 2 || out["edit_count"] != 3 {
		t.Fatalf("lost important completion facts: %#v", out)
	}
	if _, exists := out["files"]; exists {
		t.Fatalf("repeated full edit list in applied response: %#v", out)
	}
	if !reflect.DeepEqual(out["warnings"], full["warnings"]) {
		t.Fatalf("warnings were lost: %#v", out)
	}
	if _, ok := full["files"]; !ok {
		t.Fatal("compacting a response modified the original payload")
	}
}
func TestCompactAppliedRenameDoesNotHidePreview(t *testing.T) {
	preview := map[string]any{"applied": false, "files": []fileEditPreview{{Path: "a.ts"}}}
	if got := CompactAppliedRename(preview); !reflect.DeepEqual(got, preview) {
		t.Fatalf("preview data removed: %#v", got)
	}
}
