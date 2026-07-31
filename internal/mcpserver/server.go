package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/tools"
)

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

type definition struct {
	name, description string
	schema            map[string]any
	call              func(context.Context, *tools.Engine, map[string]any) (map[string]any, error)
}

func definitions() []definition {
	return []definition{
		{"search_symbols", "Use first to find a code symbol by name before any shell search. language is required and selects the LSP server.", schema("query", "language"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.SearchSymbols(c, i)
		}},
		{"list_workspace_symbols", "List methods in Go code or other workspace symbols for one language. language is required and selects the LSP server.", schema("language"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.SearchSymbols(c, i)
		}},
		{"get_document_symbols", "Get hierarchical document symbols; prefer it over reading source text. path identifies the file and required language selects the LSP server.", schema("path", "language"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.DocumentSymbols(c, i)
		}},
		{"get_symbol", "Get a symbol handle and optional source.", schema("symbol_id"), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.GetSymbol(c, i)
		}},
		{"get_definition", "Get definition locations. language is required and selects the LSP server.", targetSchema(), relation("get_definition", "textDocument/definition", "definition")},
		{"find_references", "Find reference locations. language is required and selects the LSP server.", targetSchema(), relation("find_references", "textDocument/references", "references")},
		{"find_implementations", "Find implementation locations. language is required and selects the LSP server.", targetSchema(), relation("find_implementations", "textDocument/implementation", "implementation")},
		{"get_type_definition", "Get type definition locations. language is required and selects the LSP server.", targetSchema(), relation("get_type_definition", "textDocument/typeDefinition", "typeDefinition")},
		{"get_declaration", "Get declaration locations. language is required and selects the LSP server.", targetSchema(), relation("get_declaration", "textDocument/declaration", "declaration")},
		{"get_incoming_calls", "Get direct callers. language is required and selects the LSP server.", targetSchema(), hierarchy("get_incoming_calls")},
		{"get_outgoing_calls", "Get direct callees. language is required and selects the LSP server.", targetSchema(), hierarchy("get_outgoing_calls")},
		{"get_supertypes", "Get direct supertypes. language is required and selects the LSP server.", targetSchema(), hierarchy("get_supertypes")},
		{"get_subtypes", "Get direct subtypes. language is required and selects the LSP server.", targetSchema(), hierarchy("get_subtypes")},
		{"get_diagnostics", "Get LSP diagnostics. language is required when path is supplied and selects the LSP server.", schema(), func(c context.Context, e *tools.Engine, i map[string]any) (map[string]any, error) {
			return e.Diagnostics(c, i)
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
func schema(required ...string) map[string]any {
	s := objectSchema()
	s["properties"] = inputProperties()
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func inputProperties() map[string]any {
	return map[string]any{
		"query":               stringSchema(),
		"path":                stringSchema(),
		"symbol_id":           stringSchema(),
		"line":                positiveIntegerSchema(),
		"column":              positiveIntegerSchema(),
		"limit":               positiveIntegerSchema(),
		"language":            stringSchema(),
		"kinds":               map[string]any{"type": "array", "items": stringSchema()},
		"include_source":      map[string]any{"type": "boolean"},
		"include_declaration": map[string]any{"type": "boolean"},
	}
}

func stringSchema() map[string]any { return map[string]any{"type": "string"} }

func positiveIntegerSchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 1}
}

func targetSchema() map[string]any { return schema("language") }
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
