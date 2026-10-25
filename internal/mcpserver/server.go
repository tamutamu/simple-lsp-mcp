package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/tools"
)

// New exposes precise LSP navigation and edits to coding agents.
const serverInstructions = "Use individual LSP tools only when semantic information adds value beyond file reading or text search. Search symbols, then request exactly the definitions, references, implementations, or hierarchy edges needed for the task. Do not invoke every tool by default. rename_symbol and format_document preview before applying; apply_code_action writes only the selected LSP edit. Run real tests after edits; an LSP diagnostic is not a test result."

func New(engine *tools.Engine, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "simple-lsp-mcp", Version: version}, &mcp.ServerOptions{Instructions: serverInstructions})
	for _, d := range definitions() {
		def := d
		s.AddTool(&mcp.Tool{Name: def.name, Description: def.description, InputSchema: def.schema, OutputSchema: objectSchema(), Annotations: toolAnnotations(def.name)}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var in map[string]any
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &in); err != nil {
					return result(map[string]any{"error": map[string]any{"code": core.InvalidArgument, "message": "invalid tool arguments"}}, true), nil
				}
			}
			out, err := def.call(ctx, engine, in)
			if err != nil {
				return result(map[string]any{"error": errorObject(err)}, true), nil
			}
			return result(out, false), nil
		})
	}
	return s
}

func toolAnnotations(name string) *mcp.ToolAnnotations {
	closedWorld := false
	if name == "rename_symbol" || name == "format_document" || name == "apply_code_action" {
		destructive := true
		return &mcp.ToolAnnotations{DestructiveHint: &destructive, OpenWorldHint: &closedWorld}
	}
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closedWorld}
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
		{"rename_symbol", "Preview an LSP semantic rename; apply=true writes the validated cross-file WorkspaceEdit.", objSchema(props("symbol_id", "symbol_path", "path", "line", "column", "language", "new_name", "apply"), "new_name"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.RenameSymbol(c, i)
		}},
		{"format_document", "Preview LSP document/range formatting; apply=true writes the validated edits.", objSchema(props("path", "language", "line", "column", "end_line", "end_column", "tab_size", "insert_spaces", "trim_trailing_whitespace", "insert_final_newline", "trim_final_newlines", "apply"), "path"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.FormatDocument(c, i)
		}},
		{"get_code_actions", "List LSP code actions without writing; use the returned action_id with apply_code_action.", objSchema(props("path", "language", "line", "column", "end_line", "end_column", "code_action_kinds", "limit"), "path", "line", "column"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.GetCodeActions(c, i)
		}},
		{"apply_code_action", "Apply the selected action_id only when it resolves to the same WorkspaceEdit-backed action; commands are not executed.", objSchema(props("path", "language", "line", "column", "end_line", "end_column", "code_action_kinds", "action_id"), "path", "line", "column", "action_id"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.ApplyCodeAction(c, i)
		}},
		{"get_diagnostics", "Get LSP diagnostics for a file, or cached diagnostics when path is omitted.", objSchema(props("path", "language", "severities", "limit")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.Diagnostics(c, i)
		}},

		// Discovery and direct symbol inspection.
		{"search_symbols", "Search workspace symbols by non-empty name query; language selects the LSP profile.", objSchema(props("query", "language", "kinds", "limit"), "query", "language"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.SearchSymbols(c, i)
		}},
		{"list_workspace_symbols", "Enumerate configured source symbols page by page; follow next_cursor until absent.", objSchema(props("language", "kinds", "limit", "cursor")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.ListWorkspaceSymbols(c, i)
		}},
		{"get_document_symbols", "Get a concise source-file outline by default (top-level symbols and nested declarations, excluding local variables). detail=full returns all LSP symbols and exact ranges.", objSchema(props("path", "language", "detail"), "path"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.DocumentSymbols(c, i)
		}},
		{"get_hover", "Get LSP hover/type information for an exact target.", objSchema(props("symbol_id", "symbol_path", "path", "line", "column", "language")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.Hover(c, i)
		}},

		// Ask for one exact semantic relationship at a time.
		{"get_definition", "LSP: definition locations for a target.", targetSchema(), relation("get_definition", "textDocument/definition", "definition")},
		{"find_references", "LSP: reference locations for a target.", objSchema(targetProps("include_declaration")), relation("find_references", "textDocument/references", "references")},
		{"find_implementations", "LSP: implementation locations for a target.", targetSchema(), relation("find_implementations", "textDocument/implementation", "implementation")},
		{"get_type_definition", "LSP: type-definition locations for a target.", targetSchema(), relation("get_type_definition", "textDocument/typeDefinition", "typeDefinition")},
		{"get_declaration", "LSP: declaration locations for a target.", targetSchema(), relation("get_declaration", "textDocument/declaration", "declaration")},
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
	s["properties"] = properties
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// allProperties is the full catalogue of tool input properties, each documented once.
func allProperties() map[string]any {
	return map[string]any{
		"query":                    describe(stringSchema(), "Non-empty symbol query."),
		"cursor":                   describe(stringSchema(), "next_cursor from a prior workspace-symbol page."),
		"path":                     describe(stringSchema(), "Workspace-relative file path."),
		"symbol_id":                describe(stringSchema(), "Previously returned symbol_id."),
		"symbol_path":              describe(stringSchema(), "Symbol path like UserService/createUser; a unique suffix is allowed, leading / requires exact, and path scopes the file."),
		"line":                     describe(positiveIntegerSchema(), "One-based line."),
		"column":                   describe(positiveIntegerSchema(), "One-based column."),
		"limit":                    describe(positiveIntegerSchema(), "Maximum results."),
		"language":                 describe(enumStringSchema("python", "typescript", "typescriptreact", "javascript", "javascriptreact", "go", "html", "css"), "Language; target tools infer it when possible."),
		"kinds":                    describe(map[string]any{"type": "array", "items": enumStringSchema("file", "module", "namespace", "package", "class", "method", "property", "field", "constructor", "enum", "interface", "function", "variable", "constant", "string", "number", "boolean", "array", "object", "key", "null", "enum_member", "struct", "event", "operator", "type_parameter")}, "Allowed symbol kinds."),
		"severities":               describe(map[string]any{"type": "array", "items": enumStringSchema("error", "warning", "information", "hint")}, "Allowed diagnostic severities."),
		"include_declaration":      describe(map[string]any{"type": "boolean"}, "Include declaration among references."),
		"detail":                   describe(enumStringSchema("outline", "full"), "Default outline omits local variables and repeated ranges; full returns complete LSP document symbols."),
		"new_name":                 describe(stringSchema(), "New LSP rename name."),
		"apply":                    describe(map[string]any{"type": "boolean"}, "Apply validated edits; preview tools default false."),
		"end_line":                 describe(positiveIntegerSchema(), "One-based range end line."),
		"end_column":               describe(positiveIntegerSchema(), "One-based range end column."),
		"tab_size":                 describe(positiveIntegerSchema(), "Formatting tab size; default 4."),
		"insert_spaces":            describe(map[string]any{"type": "boolean"}, "Formatting insertSpaces; default true."),
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
	b, _ := json.Marshal(v)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, StructuredContent: v, IsError: isError}
}

func errorObject(err error) map[string]any {
	if e, ok := err.(*core.AppError); ok {
		return map[string]any{"code": e.Code, "message": e.Message, "language": e.Language, "method": e.Method}
	}
	return map[string]any{"code": core.InternalError, "message": fmt.Sprint(err)}
}
