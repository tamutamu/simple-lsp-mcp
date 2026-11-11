package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func renameExample(edits int) map[string]any {
	files := make([]map[string]any, 0, 16)
	for i := 0; i < 16; i++ {
		n := 2
		if i < edits-32 {
			n++
		}
		changes := make([]map[string]any, n)
		for j := 0; j < n; j++ {
			changes[j] = map[string]any{
				"range": core.Range{
					Start: core.Position{Line: 10 + j, Column: 1},
					End:   core.Position{Line: 10 + j, Column: 4},
				},
				"new_text": "NewEngine",
			}
		}
		files = append(files, map[string]any{"path": fmt.Sprintf("pkg/file%02d.go", i), "edits": changes})
	}
	return map[string]any{"applied": true, "new_name": "NewEngine", "file_count": 16, "edit_count": edits, "files": files}
}

func TestReadableAppliedRenameCompactsButKeepsEveryAffectedFile(t *testing.T) {
	data := renameExample(43)
	input := map[string]any{"path": "pkg/engine.go", "symbol_path": "New", "apply": true}
	rendered, ok := renderReadableResult("rename_symbol", input, data)
	if !ok {
		t.Fatal("applied rename was not rendered")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(rendered) >= len(raw)/2 {
		t.Fatalf("readable result did not reduce output sufficiently: %d vs %d bytes", len(rendered), len(raw))
	}
	for i := 0; i < 16; i++ {
		if !strings.Contains(rendered, fmt.Sprintf("pkg/file%02d.go", i)) {
			t.Fatalf("affected file omitted: file%02d.go", i)
		}
	}
	for _, v := range []string{"APPLIED", "43 edits", "16 files", "NewEngine", "run the relevant tests"} {
		if !strings.Contains(rendered, v) {
			t.Fatalf("missing %s", v)
		}
	}
	if data["files"].([]map[string]any)[0]["edits"] == nil {
		t.Fatal("renderer mutated the engine's complete original result")
	}
	for _, format := range []string{"both", "text"} {
		r := resultWithFormat(data, false, format)
		content := r.Content[0].(*mcp.TextContent)
		var obj map[string]any
		if err := json.Unmarshal([]byte(content.Text), &obj); err != nil {
			t.Fatal(err)
		}
		if obj["edit_count"] != float64(43) {
			t.Fatalf("old %s API lost edits", format)
		}
	}
}

func TestReadableRenameNeverHidesUnsupportedOrFailure(t *testing.T) {
	original := renameExample(43)
	for _, mutation := range []func(map[string]any){
		func(d map[string]any) { d["applied"] = false },
		func(d map[string]any) { d["edit_count"] = 44 },
		func(d map[string]any) { d["file_count"] = 15 },
		func(d map[string]any) { d["warnings"] = []string{"stale file"} },
		func(d map[string]any) { d["unknown_field"] = "never hide" },
		func(d map[string]any) { d["files"] = []any{} },
	} {
		d := make(map[string]any)
		for k, v := range original {
			d[k] = v
		}
		mutation(d)
		if text, ok := renderReadableResult("rename_symbol", map[string]any{"apply": true}, d); ok {
			t.Fatalf("unsafe rename result rendered instead of falling back to JSON: %s", text)
		}
	}
	if _, ok := renderReadableResult("rename_symbol", map[string]any{"apply": false}, original); ok {
		t.Fatal("preview from write tool should preserve exact edit response")
	}
}

func TestReadableRenamePreviewRespectsFullAudit(t *testing.T) {
	files := []map[string]any{{
		"path": "pkg/engine.go", "edit_count": 3,
		"omitted_edits": 2,
		"sample_edits":  []map[string]any{{"range": core.Range{Start: core.Position{Line: 7, Column: 9}}, "new_text": "NewEngine"}},
	}}
	d := map[string]any{
		"applied": false, "new_name": "NewEngine", "edit_count": 3, "file_count": 1,
		"files": files, "preview_detail": "summary", "preview_truncated": true,
		"sample_edit_count": 1, "full_preview_available": "preview_rename(preview_detail=full)",
	}
	rendered, ok := renderReadableResult("preview_rename", map[string]any{}, d)
	if !ok || !strings.Contains(rendered, "READ ONLY") ||
		!strings.Contains(rendered, "pkg/engine.go") || !strings.Contains(rendered, "7:9") ||
		!strings.Contains(rendered, "2 other edits not shown") {
		t.Fatalf("summary not usefully rendered: %s", rendered)
	}
	if _, ok := renderReadableResult("preview_rename", map[string]any{"preview_detail": "full"}, d); ok {
		t.Fatal("explicit full-audit requests must not be compacted")
	}
}
