package tools

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
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
func TestGoRenameInterfaceContractErrorIsActionable(t *testing.T) {
	original := errors.New("lsp error 0: renaming this method \"EmitAuditEvent\" would make *Log no longer assignable to interface IAuditLog")
	actual := classifySemanticRenameError("go", original)
	if !strings.Contains(actual.Error(), "rename the corresponding interface method") {
		t.Fatalf("interface rename guidance absent: %v", actual)
	}
	if got := classifySemanticRenameError("typescript-javascript", original); got != original {
		t.Fatalf("changed unrelated language error: %v", got)
	}
	if got := classifySemanticRenameError("go", errors.New("ordinary lsp error")); got.Error() != "ordinary lsp error" {
		t.Fatalf("changed unrelated error: %v", got)
	}
}
func TestCompactPlanningPreviewKeepsCoverageButDropsEditRanges(t *testing.T) {
	original := map[string]any{
		"applied": false, "file_count": 2, "edit_count": 3,
		"files": []map[string]any{
			{"path": "a.go", "edit_count": 2, "sample_edits": []editPreview{{NewText: "After"}}},
			{"path": "b.go", "edit_count": 1, "sample_edits": []editPreview{{NewText: "After"}}},
		},
		"unrenamed_method_candidates": []renameSiblingCandidate{{Path: "c.go", SymbolPath: "Other/Before"}},
		"unverified_build_tag_files":  []string{"feature_linux.go"},
		"coverage_complete":           false,
		"warnings":                    []string{"do not claim coverage"},
		"sample_edit_count":           2,
		"full_preview_available":      "preview_rename(preview_detail=full)",
	}
	got := CompactPlanningRenamePreview(original)
	raw, err := json.Marshal(got["files"])
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `[{"path":"a.go","edit_count":2},{"path":"b.go","edit_count":1}]` {
		t.Fatalf("unexpected file-only summary: %s", raw)
	}
	if got["coverage_complete"] != false ||
		!reflect.DeepEqual(got["warnings"], original["warnings"]) ||
		!reflect.DeepEqual(got["unrenamed_method_candidates"], original["unrenamed_method_candidates"]) ||
		!reflect.DeepEqual(got["unverified_build_tag_files"], original["unverified_build_tag_files"]) {
		t.Fatalf("lost safety-critical output: %#v", got)
	}
	if _, ok := got["sample_edit_count"]; ok {
		t.Fatal("unnecessary samples counted")
	}
	if _, ok := got["full_preview_available"]; ok {
		t.Fatal("full preview not on narrow profile")
	}
	if _, ok := original["sample_edit_count"]; !ok {
		t.Fatal("original payload mutated")
	}
}
