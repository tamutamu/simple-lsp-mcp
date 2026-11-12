package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/tools"
)

// New exposes precise LSP navigation and edits to coding agents.
const serverInstructions = "Known symbol: inspect_symbol(view=edit,path,symbol_path) directly. Unknown: get_document_symbols(path), reuse exact symbol_id; do not guess. Batch only verified targets. For exhaustive references use find_references (production-only: exclude_test_files=true); inspect_symbol(related) is bounded to 20 per kind, not exhaustive. For type errors get_diagnostics(path); for exact literals rg is cheaper. Rename: preview_rename summary; full for audit only; then rename_symbol(apply=true); for format preview_format then format_document(apply=true). Reuse source until edited; avoid duplicate reads. Do not invoke every tool. Run actual tests."

func New(engine *tools.Engine, version string) *mcp.Server {
	return NewWithProfile(engine, version, "full")
}

// NewWithProfile preserves the full API by default. The opt-in "coding"
// profile exposes frequent navigation and semantic rename operations, while
// omitting uncommon format, code-action, and type-hierarchy schemas. The omitted tools remain in "full".
func NewWithProfile(engine *tools.Engine, version, profile string) *mcp.Server {
	return NewWithOptions(engine, version, profile, "both")
}

// NewWithOptions allows an opt-in text-only result. Keeping the dual format
// as the default preserves structured MCP results for existing clients.
// A text-only tool must not advertise an output schema requiring
// structuredContent, per the MCP specification.
func NewWithOptions(engine *tools.Engine, version, profile, resultFormat string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "simple-lsp-mcp", Title: "Simple LSP semantic code navigation", Version: version, WebsiteURL: "https://github.com/tamutamu/simple-lsp-mcp"}, &mcp.ServerOptions{Instructions: serverInstructions})
	for _, d := range definitionsForProfile(profile) {
		def := d
		s.AddTool(&mcp.Tool{Name: def.name, Title: toolTitle(def.name), Description: def.description, InputSchema: def.schema, OutputSchema: outputSchemaForFormat(resultFormat), Annotations: toolAnnotations(def.name)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// A tool without required fields accepts absent arguments.
			// JSON null is not an object and remains invalid.
			in := map[string]any{}
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &in); err != nil {
					return resultWithFormat(map[string]any{"error": map[string]any{"code": core.InvalidArgument, "message": "invalid tool arguments"}}, true, resultFormat), nil
				}
			}
			if err := validateToolArguments(def.schema, in); err != nil {
				return resultWithFormat(map[string]any{"error": errorObject(err)}, true, resultFormat), nil
			}
			if err := validateToolProfileArguments(profile, def.name, in); err != nil {
				return resultWithFormat(map[string]any{"error": errorObject(err)}, true, resultFormat), nil
			}
			out, err := def.call(ctx, engine, in)
			if err != nil {
				return resultWithFormat(map[string]any{"error": errorObject(err)}, true, resultFormat), nil
			}
			if resultFormat == "readable" {
				if formatted, ok := renderReadableResult(def.name, in, out); ok {
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: formatted}}}, nil
				}
			}
			return resultWithFormat(out, false, resultFormat), nil
		})
	}
	return s
}

// validateToolProfileArguments checks profile-specific promises even when the
// MCP client does not enforce JSON Schema's required fields itself.
func validateToolProfileArguments(profile, name string, in map[string]any) error {
	if profile == "coding" && name == "get_diagnostics" {
		path, _ := in["path"].(string)
		if strings.TrimSpace(path) == "" {
			return core.NewError(core.InvalidArgument,
				"get_diagnostics requires path in the coding profile; specify a known workspace-relative source file")
		}
	}
	return nil
}

// toolTitle supplies a human-readable discovery label without changing stable tool names.
func toolTitle(name string) string {
	words := strings.Split(name, "_")
	if len(words) == 0 || words[0] == "" {
		return name
	}
	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	return strings.Join(words, " ")
}

func toolAnnotations(name string) *mcp.ToolAnnotations {
	closedWorld := false
	if name == "rename_symbol" || name == "format_document" || name == "apply_code_action" {
		destructive := true
		return &mcp.ToolAnnotations{DestructiveHint: &destructive, OpenWorldHint: &closedWorld}
	}
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld}
}

// definitionsForProfile only filters the exposed schema. It never changes
// LSP resolution, stored identities, or behavior of exposed operations.
// All semantic refactors and advanced relationship tools stay accessible via
// --tool-profile full.
func definitionsForProfile(profile string) []definition {
	all := definitions()
	if profile != "coding" {
		return all
	}
	allowed := map[string]bool{
		"search_symbols": true, "list_workspace_symbols": true,
		"get_document_symbols": true, "inspect_symbol": true,
		"get_hover": true, "get_definition": true,
		"find_references": true, "find_implementations": true,
		"get_diagnostics": true, "get_incoming_calls": true,
		"get_outgoing_calls": true,
		"analyze_impact":     true,
		"preview_rename":     true,
		"rename_symbol":      true,
	}
	out := make([]definition, 0, len(allowed))
	for _, d := range all {
		if !allowed[d.name] {
			continue
		}
		if d.name == "get_diagnostics" {
			// The lean agent profile requires a concrete file. An omitted
			// path produces only an explicitly incomplete status, not a
			// useful workspace diagnostic; prevent an unnecessary RPC.
			d.schema = objSchema(props("path", "language", "severities", "limit"), "path")
		}
		out = append(out, d)
	}
	return out
}

// definition binds MCP metadata to an engine operation.
type definition struct {
	name, description string
	schema            map[string]any
	call              func(context.Context, *tools.Engine, map[string]any) (map[string]any, error)
}

// definitions is the stable public tool surface exposed by this server.
func definitions() []definition {
	return []definition{
		// Exact LSP operations. The agent chooses which relation to inspect.
		{"rename_symbol", "Apply an LSP semantic rename only with apply=true (writes files); use preview_rename first for a read-only preview.", objSchema(props("symbol_id", "symbol_path", "path", "line", "column", "language", "new_name", "apply"), "new_name"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.RenameSymbol(c, i)
		}},
		{"preview_rename", "READ ONLY: preview semantic rename. Omit preview_detail to get ALL affected file paths and counts plus samples. Request full only to inspect every edit, not to confirm file scope. Apply separately with rename_symbol.", objSchema(props("symbol_id", "symbol_path", "path", "line", "column", "language", "new_name", "preview_detail"), "new_name"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.PreviewRename(c, i)
		}},
		{"format_document", "Apply validated document/range formatting only with apply=true (writes files); use preview_format first.", objSchema(props("path", "language", "line", "column", "end_line", "end_column", "tab_size", "insert_spaces", "trim_trailing_whitespace", "insert_final_newline", "trim_final_newlines", "apply"), "path"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.FormatDocument(c, i)
		}},
		{"preview_format", "READ ONLY: preview exact LSP document/range formatting edits, without writing files or requiring approval.", objSchema(props("path", "language", "line", "column", "end_line", "end_column", "tab_size", "insert_spaces", "trim_trailing_whitespace", "insert_final_newline", "trim_final_newlines"), "path"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.PreviewFormat(c, i)
		}},
		{"get_code_actions", "List LSP code actions without writing; use the returned action_id with apply_code_action.", objSchema(props("path", "language", "line", "column", "end_line", "end_column", "code_action_kinds", "limit"), "path", "line", "column"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.GetCodeActions(c, i)
		}},
		{"apply_code_action", "Apply the selected action_id only when it resolves to the same WorkspaceEdit-backed action; commands are not executed.", objSchema(props("path", "language", "line", "column", "end_line", "end_column", "code_action_kinds", "action_id"), "path", "line", "column", "action_id"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.ApplyCodeAction(c, i)
		}},
		{"get_diagnostics", "For type errors, get warmed LSP diagnostics for a file with exact lines, compiler message, and completeness; fix from evidence then verify with tests. Always pass path for current diagnostics: an unscoped request returns incomplete status, not a clean bill of health.", objSchema(props("path", "language", "severities", "limit")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.Diagnostics(c, i)
		}},

		{"analyze_impact", "Recursive LSP reference graph. path+line; depth=1 direct, 2 next hop (default 8, max 16). Cycles and truncation reported.", objSchema(props("path", "line", "depth", "max_nodes"), "path", "line"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.AnalyzeImpact(c, i)
		}},

		// Discovery and direct symbol inspection.
		{"search_symbols", "Find semantically identified declarations; return only name/kind/path/line/handle by default, detail=full for exact ranges.", objSchema(props("query", "language", "kinds", "limit", "detail"), "query", "language"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.SearchSymbols(c, i)
		}},
		{"list_workspace_symbols", "Enumerate compact symbol identities page by page; follow next_cursor until absent. detail=full adds complete ranges.", objSchema(props("language", "kinds", "limit", "cursor", "detail")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.ListWorkspaceSymbols(c, i)
		}},
		{"get_document_symbols", "Discover names and structure in an unfamiliar file. Skip this before inspect_symbol when the function name is already known.", objSchema(props("path", "language", "detail"), "path"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.DocumentSymbols(c, i)
		}},
		{"get_hover", "Get LSP hover/type information for an exact target.", objSchema(props("symbol_id", "symbol_path", "path", "line", "column", "language")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.Hover(c, i)
		}},
		{"inspect_symbol", "Inspect one exact declaration or batch verified targets[]. related[] returns only bounded context (maximum 20 per relation), never an exhaustive reference list. For all usages call find_references instead. If name is unknown, discover get_document_symbols(path) first; do not guess.", objSchema(props("symbol_id", "symbol_path", "path", "line", "column", "language", "view", "max_lines", "targets", "related", "relation_limit")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.InspectSymbol(c, i)
		}},

		// Ask for one exact semantic relationship at a time.
		{"get_definition", "LSP: definition locations for a target.", objSchema(targetProps("relation_view")), relation("get_definition", "textDocument/definition", "definition")},
		{"find_references", "Exact LSP references. If path and symbol_path are known, call directly, without preview_rename or inspect_symbol. Use relation_view=locations for path-only impact; lines adds source text. exclude_test_files=true opts out of test hits, reporting excluded_count.", objSchema(targetProps("include_declaration", "relation_view", "exclude_test_files")), relation("find_references", "textDocument/references", "references")},
		{"find_implementations", "LSP: implementation locations for a target.", objSchema(targetProps("relation_view")), relation("find_implementations", "textDocument/implementation", "implementation")},
		{"get_type_definition", "LSP: type-definition locations for a target.", objSchema(targetProps("relation_view")), relation("get_type_definition", "textDocument/typeDefinition", "typeDefinition")},
		{"get_declaration", "LSP: declaration locations for a target.", objSchema(targetProps("relation_view")), relation("get_declaration", "textDocument/declaration", "declaration")},
		{"get_incoming_calls", "LSP: direct callers.", targetSchema(), hierarchy("get_incoming_calls")},
		{"get_outgoing_calls", "LSP: direct callees.", targetSchema(), hierarchy("get_outgoing_calls")},
		{"get_supertypes", "LSP: direct supertypes.", targetSchema(), hierarchy("get_supertypes")},
		{"get_subtypes", "LSP: direct subtypes.", targetSchema(), hierarchy("get_subtypes")},
	}
}

func relation(name, method, cap string) func(context.Context, *tools.Engine, map[string]any) (map[string]any, error) {
	return func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
		return e.Relationship(c, name, method, cap, i)
	}
}

func hierarchy(name string) func(context.Context, *tools.Engine, map[string]any) (map[string]any, error) {
	return func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
		return e.Hierarchy(c, name, i)
	}
}

func objectSchema() map[string]any { return map[string]any{"type": "object"} }

// objSchema builds an input schema exposing exactly the named properties.
func objSchema(properties map[string]any, required ...string) map[string]any {
	s := objectSchema()
	s["additionalProperties"] = false
	s["properties"] = properties
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// allProperties is the full catalogue of tool input properties, each documented once.
func allProperties() map[string]any {
	return map[string]any{
		"query":               describe(stringSchema(), "Non-empty symbol query."),
		"cursor":              describe(stringSchema(), "next_cursor from a prior workspace-symbol page."),
		"path":                describe(stringSchema(), "Workspace-relative path."),
		"symbol_id":           describe(stringSchema(), "Returned symbol_id."),
		"symbol_path":         describe(stringSchema(), "Nested name; leading / anchors, path scopes."),
		"line":                describe(positiveIntegerSchema(), "1-based line."),
		"column":              describe(positiveIntegerSchema(), "1-based column."),
		"limit":               describe(positiveIntegerSchema(), "Max results."),
		"depth":               describe(positiveIntegerSchema(), "1=direct, 2=next hop; default 8, maximum 16."),
		"max_nodes":           describe(positiveIntegerSchema(), "Maximum graph nodes; default 80, maximum 200."),
		"language":            describe(enumStringSchema("python", "typescript", "typescriptreact", "javascript", "javascriptreact", "go", "html", "css"), "Language; inferred from path when possible."),
		"kinds":               describe(map[string]any{"type": "array", "items": enumStringSchema("file", "module", "namespace", "package", "class", "method", "property", "field", "constructor", "enum", "interface", "function", "variable", "constant", "string", "number", "boolean", "array", "object", "key", "null", "enum_member", "struct", "event", "operator", "type_parameter")}, "Allowed symbol kinds."),
		"severities":          describe(map[string]any{"type": "array", "items": enumStringSchema("error", "warning", "information", "hint")}, "Allowed diagnostic severities."),
		"include_declaration": describe(map[string]any{"type": "boolean"}, "Include declaration among references."),
		"exclude_test_files":  describe(map[string]any{"type": "boolean"}, "Opt-in: omit _test.go, test_*.py, *.test.ts/js and other known test paths from exact LSP references. Results report excluded_count; default false."),
		"detail":              describe(enumStringSchema("compact", "outline", "full"), "compact (default), outline or full LSP details."),
		"view":                describe(enumStringSchema("source", "signature", "location", "edit"), "source/default, edit/context, signature/header or location/range."),
		"relation_view":       describe(enumStringSchema("locations", "lines"), "locations (default): exact LSP positions; lines: also include a bounded source line at every result."),
		"max_lines":           describe(positiveIntegerSchema(), "Source lines, default 120, max 500."),
		"related":             describe(map[string]any{"type": "array", "minItems": 1, "maxItems": 6, "uniqueItems": true, "items": enumStringSchema("references", "implementations", "type_definition", "declaration", "incoming_calls", "outgoing_calls", "supertypes", "subtypes")}, "Bounded context (max 20 per kind); full refs: find_references."),
		"relation_limit":      describe(map[string]any{"type": "integer", "minimum": 1, "maximum": 20}, "Per-relation cap 1–20, default 8; for exhaustive usages use find_references."),
		"targets": describe(map[string]any{"type": "array", "minItems": 1, "maxItems": 8,
			"items": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
				"path": stringSchema(), "symbol_id": stringSchema(),
				"symbol_path": stringSchema(), "line": positiveIntegerSchema(),
				"column": positiveIntegerSchema(), "language": stringSchema(),
			}}}, "Batch 1–8, shared view; no top-level target."),
		"preview_detail":           describe(enumStringSchema("summary", "full"), "Rename preview size: summary (default; sample when more than 12 edits), full (every validated edit)."),
		"new_name":                 describe(stringSchema(), "New LSP rename name."),
		"apply":                    describe(map[string]any{"type": "boolean"}, "Apply validated edits; preview tools default false."),
		"end_line":                 describe(positiveIntegerSchema(), "One-based range end line."),
		"end_column":               describe(positiveIntegerSchema(), "One-based range end column."),
		"tab_size":                 describe(positiveIntegerSchema(), "Formatting tab size; default 4."),
		"insert_spaces":            describe(map[string]any{"type": "boolean"}, "Format spaces; default true."),
		"trim_trailing_whitespace": describe(map[string]any{"type": "boolean"}, "Formatting trimTrailingWhitespace."),
		"insert_final_newline":     describe(map[string]any{"type": "boolean"}, "Formatting insertFinalNewline."),
		"trim_final_newlines":      describe(map[string]any{"type": "boolean"}, "Formatting trimFinalNewlines."),
		"code_action_kinds":        describe(map[string]any{"type": "array", "items": stringSchema()}, "Requested code-action kinds."),
		"action_id":                describe(stringSchema(), "action_id returned by get_code_actions."),
	}
}

// props selects a subset of allProperties by name, in a stable field order.
func props(names ...string) map[string]any {
	all := allProperties()
	out := make(map[string]any, len(names))
	for _, n := range names {
		out[n] = all[n]
	}
	return out
}

// targetProps is the symbol_id-or-position target shared by relation and hierarchy tools,
// plus any tool-specific properties.
func targetProps(extra ...string) map[string]any {
	return props(append([]string{"symbol_id", "symbol_path", "path", "line", "column", "language", "limit"}, extra...)...)
}

func describe(s map[string]any, description string) map[string]any {
	s["description"] = description
	return s
}

func stringSchema() map[string]any { return map[string]any{"type": "string"} }

func enumStringSchema(values ...string) map[string]any {
	enum := make([]any, len(values))
	for i, value := range values {
		enum[i] = value
	}
	return map[string]any{"type": "string", "enum": enum}
}

func positiveIntegerSchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 1}
}

func targetSchema() map[string]any { return objSchema(targetProps()) }

func result(v map[string]any, isError bool) *mcp.CallToolResult {
	return resultWithFormat(v, isError, "both")
}

// Only the compatible default includes structuredContent. Text and readable
// modes return one text block; readable falls back to JSON here when a result
// cannot be rendered safely.
func resultWithFormat(v map[string]any, isError bool, format string) *mcp.CallToolResult {
	b, _ := json.Marshal(v)
	out := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, IsError: isError}
	if format == "both" {
		out.StructuredContent = v
	}
	return out
}

func outputSchemaForFormat(format string) any {
	if format != "both" {
		return nil
	}
	return objectSchema()
}

func errorObject(err error) map[string]any {
	if e, ok := err.(*core.AppError); ok {
		return map[string]any{"code": e.Code, "message": e.Message, "language": e.Language, "method": e.Method}
	}
	return map[string]any{"code": core.InternalError, "message": fmt.Sprint(err)}
}
