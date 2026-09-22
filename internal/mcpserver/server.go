package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/tools"
)

// New registers the read-only tool surface on an MCP server.
func New(engine *tools.Engine, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "simple-lsp-mcp", Version: version}, nil)
	for _, d := range definitions() {
		def := d
		s.AddTool(&mcp.Tool{Name: def.name, Description: def.description, InputSchema: def.schema, OutputSchema: objectSchema()}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

// definition binds MCP metadata to an engine operation.
type definition struct {
	name, description string
	schema            map[string]any
	call              func(context.Context, *tools.Engine, map[string]any) (map[string]any, error)
}

// definitions is the stable public tool surface exposed by this server.
func definitions() []definition {
	return []definition{
		{"search_symbols", "Use first to find a code symbol by name before any shell search. query must be non-empty; language is required and selects the LSP server.", objSchema(props("query", "language", "kinds", "limit"), "query", "language"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.SearchSymbols(c, i)
		}},
		{"list_workspace_symbols", "Enumerate all source symbols across configured languages without a name query. Uses per-file LSP document symbols and returns bounded pages; follow next_cursor until absent. Optional language and kinds filter the list.", objSchema(props("language", "kinds", "limit", "cursor")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.ListWorkspaceSymbols(c, i)
		}},
		{"get_document_symbols", "Get hierarchical document symbols; prefer it over reading source text. path identifies the file; language is inferred from its extension unless explicitly supplied.", objSchema(props("path", "language"), "path"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.DocumentSymbols(c, i)
		}},
		{"get_symbol", "Get a previously acquired symbol_id and its source.", objSchema(props("symbol_id", "include_source"), "symbol_id"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.GetSymbol(c, i)
		}},
		{"find_symbol", "Get one symbol by its human-readable symbol_path such as \"UserService/createUser\", without knowing its file position. Supply path when known for a single-LSP lookup. language is optional: it is inferred from path, or a bare symbol_path is searched across configured LSP profiles. Ambiguous results carry candidates instead of failing.", objSchema(props("symbol_path", "path", "language", "include_source", "max_source_lines", "limit"), "symbol_path"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.FindSymbol(c, i)
		}},
		{"get_symbol_outline", "List the direct children of a symbol_path, or the top-level symbols of a file. Returns names, kinds, signatures, and ranges only, never source text. language is optional and inferred when possible.", objSchema(props("symbol_path", "path", "language", "depth", "limit")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.SymbolOutline(c, i)
		}},
		{"get_symbol_context", "Get everything about one symbol in a single call: its source, callers, callees, references, and implementations. Prefer this over separate navigation calls. Target it with symbol_path, symbol_id, or path+line+column. Use include to narrow sections. language is optional and inferred when possible.", objSchema(props("symbol_path", "symbol_id", "path", "line", "column", "language", "include", "limit", "max_source_lines")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.SymbolContext(c, i)
		}},
		{"get_semantic_slice", "Build a byte-budgeted semantic code slice for one symbol: root source, bounded callee source, type definitions, test-file candidates proven by references, callers, and implementations. Use this when an agent needs enough code to understand or change a symbol in one call. language is optional and inferred when possible.", objSchema(props("symbol_id", "symbol_path", "path", "line", "column", "language", "depth", "limit", "max_source_lines", "max_bytes")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.SemanticSlice(c, i)
		}},
		{"get_definition", "Get definition locations. language is optional and inferred when possible.", targetSchema(), relation("get_definition", "textDocument/definition", "definition")},
		{"find_references", "Find reference locations. language is optional and inferred when possible.", objSchema(targetProps("include_declaration")), relation("find_references", "textDocument/references", "references")},
		{"find_implementations", "Find implementation locations. language is optional and inferred when possible.", targetSchema(), relation("find_implementations", "textDocument/implementation", "implementation")},
		{"get_type_definition", "Get type definition locations. language is optional and inferred when possible.", targetSchema(), relation("get_type_definition", "textDocument/typeDefinition", "typeDefinition")},
		{"get_declaration", "Get declaration locations. language is optional and inferred when possible.", targetSchema(), relation("get_declaration", "textDocument/declaration", "declaration")},
		{"get_hover", "Get the type a language server infers for an expression at this exact position, even when that expression has no declaration of its own. For a named symbol prefer get_symbol or get_symbol_context. language is optional and inferred when possible.", objSchema(props("symbol_id", "symbol_path", "path", "line", "column", "language")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.Hover(c, i)
		}},
		{"get_incoming_calls", "Get direct callers. language is optional and inferred when possible.", targetSchema(), hierarchy("get_incoming_calls")},
		{"get_outgoing_calls", "Get direct callees. language is optional and inferred when possible.", targetSchema(), hierarchy("get_outgoing_calls")},
		{"get_supertypes", "Get direct supertypes. language is optional and inferred when possible.", targetSchema(), hierarchy("get_supertypes")},
		{"get_subtypes", "Get direct subtypes. language is optional and inferred when possible.", targetSchema(), hierarchy("get_subtypes")},
		{"get_diagnostics", "Get LSP diagnostics for one file, or every file with cached diagnostics when path is omitted. language is inferred from path when omitted.", objSchema(props("path", "language", "severities", "limit")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.Diagnostics(c, i)
		}},
		{"impact_analysis", "Estimate the blast radius of changing a symbol: direct and transitive callers, references, implementations, and affected files. Prefer this over grepping before a refactor. Traversal is budgeted and language is inferred when omitted.", objSchema(props("symbol_id", "symbol_path", "path", "line", "column", "language", "depth", "limit", "include_references", "include_implementations")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.ImpactAnalysis(c, i)
		}},
		{"onboard", "Scan workspace for projects (Go, Python, TypeScript, etc.) and generate .simple-lsp.yaml configuration.", objSchema(props("workspace", "overwrite")), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.Onboard(c, i)
		}},
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
		"query":                   describe(stringSchema(), "Non-empty substring or fuzzy name to search for."),
		"cursor":                  describe(stringSchema(), "Pass next_cursor from a previous list_workspace_symbols response to continue enumerating. Restart without cursor if the workspace changes."),
		"path":                    describe(stringSchema(), "File path relative to the workspace root."),
		"symbol_id":               describe(stringSchema(), "A symbol_id previously returned by another tool call. Fails with a stale-symbol error if the file changed since it was issued."),
		"symbol_path":             describe(stringSchema(), "Human-readable symbol path such as \"UserService/createUser\": the names of the enclosing symbols and the symbol itself, joined by \"/\". A trailing portion alone (\"createUser\") is accepted when it is unambiguous. Prefix with \"/\" to require an exact match instead of a trailing one. Escape a literal \"/\" inside a name as \"%2F\". Combine with path to restrict the search to one file."),
		"line":                    describe(positiveIntegerSchema(), "One-based line number of the target position. Requires path and column; ignored if symbol_id is set."),
		"column":                  describe(positiveIntegerSchema(), "One-based column number of the target position. Requires path and line; ignored if symbol_id is set."),
		"limit":                   describe(positiveIntegerSchema(), "Maximum number of results to return. Defaults to the server's --max-results."),
		"language":                describe(stringSchema(), "Optional for symbol/position tools. One of: python, typescript, typescriptreact, javascript, javascriptreact, go, html, css. When omitted, simple-lsp infers it from symbol_id, path extension, or configured profiles for a bare symbol_path."),
		"kinds":                   describe(map[string]any{"type": "array", "items": describe(stringSchema(), "One of: file, module, namespace, package, class, method, property, field, constructor, enum, interface, function, variable, constant, string, number, boolean, array, object, key, null, enum_member, struct, event, operator, type_parameter.")}, "Restrict results to these symbol kinds. Omit or leave empty to allow every kind."),
		"severities":              describe(map[string]any{"type": "array", "items": describe(stringSchema(), "One of: error, warning, information, hint.")}, "Restrict results to these diagnostic severities. Omit or leave empty to allow every severity."),
		"include_source":          describe(map[string]any{"type": "boolean"}, "Include the symbol's source text in the result. Defaults to true."),
		"include_declaration":     describe(map[string]any{"type": "boolean"}, "Include the declaration itself among the references. Defaults to false."),
		"overwrite":               describe(map[string]any{"type": "boolean"}, "Overwrite an existing .simple-lsp.yaml if present. Defaults to false."),
		"workspace":               describe(stringSchema(), "Target workspace directory to scan. Defaults to the server's configured workspace root."),
		"max_source_lines":        describe(positiveIntegerSchema(), "Maximum number of source lines to include. Defaults to 200."),
		"max_bytes":               describe(positiveIntegerSchema(), "Maximum serialized JSON response size in bytes for get_semantic_slice. Defaults to 24576; valid range 2048-81920. This is not a token count."),
		"depth":                   describe(positiveIntegerSchema(), "How many levels of children to return. Defaults to 1 (direct children only). Maximum 3."),
		"include":                 describe(map[string]any{"type": "array", "items": describe(stringSchema(), "One of: source, incoming_calls, outgoing_calls, references, implementations.")}, "Which sections to gather. Omit or leave empty to get every section."),
		"include_references":      describe(map[string]any{"type": "boolean"}, "Include reference locations in the impact set. Defaults to true."),
		"include_implementations": describe(map[string]any{"type": "boolean"}, "Include implementation locations in the impact set. Defaults to true."),
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
