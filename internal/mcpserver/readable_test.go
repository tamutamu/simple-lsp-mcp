package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestReadableInspectUsesLiteralSource(t *testing.T) {
	text, ok := renderReadableResult("inspect_symbol", nil, map[string]any{
		"name": "load", "kind": "function", "path": "pkg/service.go",
		"range": core.Range{Start: core.Position{Line: 7, Column: 1}},
		"code":  "func load() int {\n\treturn 42\n}\n",
		"meta":  core.Meta{Complete: true},
	})
	if !ok {
		t.Fatal("inspect result was not rendered")
	}
	for _, want := range []string{"pkg/service.go:7", "```go\nfunc load() int", "\treturn 42\n"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in readable result:\n%s", want, text)
		}
	}
	if strings.Contains(text, `\n\treturn`) {
		t.Fatalf("source remained JSON escaped:\n%s", text)
	}
}

func TestReadableHoverPreservesLSPMarkdown(t *testing.T) {
	text, ok := renderReadableResult("get_hover", nil, map[string]any{
		"hover": map[string]any{
			"path": "src/task.ts", "contents": "```typescript\nconst task: Task\n```\n",
			"range": core.Range{Start: core.Position{Line: 11, Column: 7}},
		},
		"meta": core.Meta{Complete: true},
	})
	if !ok || !strings.Contains(text, "src/task.ts:11:7") || !strings.Contains(text, "```typescript\nconst task: Task") {
		t.Fatalf("hover Markdown was not rendered: %q", text)
	}
}

func TestReadableSemanticRelationShowsLinesAndSource(t *testing.T) {
	lineText, ok := renderReadableResult("find_references", map[string]any{"relation_view": "lines"}, map[string]any{
		"locations": []core.Location{{
			Path: "pkg/service.go", Range: core.Range{Start: core.Position{Line: 9, Column: 3}}, Preview: "  load()",
		}},
		"meta": core.Meta{Complete: true},
	})
	if !ok || !strings.Contains(lineText, "pkg/service.go:9:3   load()") {
		t.Fatalf("line relation was not rendered:\n%s", lineText)
	}

	sourceText, ok := renderReadableResult("get_definition", map[string]any{"relation_view": "source"}, map[string]any{
		"locations": []map[string]any{{
			"path":   "pkg/service.go",
			"range":  map[string]any{"start": map[string]any{"line": 7, "column": 1}},
			"source": "func load() int {\n\treturn 42\n}\n",
		}},
	})
	if !ok || !strings.Contains(sourceText, "func load() int {\n\treturn 42") {
		t.Fatalf("source relation was not rendered:\n%s", sourceText)
	}
}

func TestReadableFilteredReferencesDisclosesScopeAndPreservesPositions(t *testing.T) {
	item := core.Location{
		Path:    "cmd/app/main.go",
		Range:   core.Range{Start: core.Position{Line: 42, Column: 17}, End: core.Position{Line: 42, Column: 20}},
		Preview: "  engine := tools.New(ws,cfg)", SymbolID: "id-123",
	}
	result, ok := renderReadableResult("find_references", map[string]any{"relation_view": "lines"}, map[string]any{
		"locations": []core.Location{item},
		"filters":   map[string]any{"exclude_test_files": true, "excluded_count": 14},
		"meta":      core.Meta{Complete: true},
	})
	for _, v := range []string{"test files excluded (14 references omitted", "cmd/app/main.go:42:17", "engine := tools.New", "symbol_id=id-123"} {
		if !ok || !strings.Contains(result, v) {
			t.Fatalf("lost %q in %s", v, result)
		}
	}
}

func TestReadableFallbackIsJSONTextWithoutStructuredContent(t *testing.T) {
	if _, ok := renderReadableResult("rename_symbol", nil, map[string]any{"applied": true}); ok {
		t.Fatal("non-renderable result should request JSON fallback")
	}
	payload := map[string]any{"error": map[string]any{"code": "EXAMPLE"}}
	result := resultWithFormat(payload, true, "readable")
	if !result.IsError || result.StructuredContent != nil || outputSchemaForFormat("readable") != nil {
		t.Fatalf("readable fallback contract changed: %#v", result)
	}
	content, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("fallback content type = %T", result.Content[0])
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(content.Text), &decoded); err != nil {
		t.Fatalf("fallback is not JSON: %v", err)
	}
}

func TestReadablePreservesExtraSemanticInformation(t *testing.T) {
	// Related relationships must not be silently dropped by a source-only renderer.
	context := map[string]any{"path": "pkg/service.go", "name": "load", "kind": "function", "code": "func load() {}", "meta": core.Meta{Complete: true},
		"related": map[string]any{"references": []any{}}, "related_meta": map[string]any{"complete": true}}
	if _, ok := renderReadableResult("inspect_symbol", map[string]any{"view": "edit"}, context); ok {
		t.Fatal("related semantic data was hidden by readable renderer")
	}
	delete(context, "related")
	delete(context, "related_meta")
	context["meta"] = core.Meta{Complete: false, Warnings: []string{"incomplete symbols"}}
	if _, ok := renderReadableResult("inspect_symbol", nil, context); ok {
		t.Fatal("incomplete LSP result must fall back to JSON with warnings")
	}
	context["meta"] = core.Meta{Complete: true}
	if _, ok := renderReadableResult("inspect_symbol", map[string]any{"view": "location"}, context); ok {
		t.Fatal("location-only output must retain all target metadata")
	}
	context["symbol_id"] = "handle-123"
	result, ok := renderReadableResult("inspect_symbol", map[string]any{"view": "source"}, context)
	if !ok || !strings.Contains(result, "Symbol handle: handle-123") {
		t.Fatalf("symbol handle lost: %q", result)
	}
}
