package tools

import (
	"context"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

// FormatDocument asks the configured language server for document formatting
// edits. It previews by default and applies only when apply=true.
func (e *Engine) FormatDocument(ctx context.Context, in map[string]any) (map[string]any, error) {
	path := stringVal(in, "path")
	if path == "" {
		return nil, core.NewError(core.InvalidArgument, "path is required")
	}
	p, s, d, err := e.document(ctx, path, stringVal(in, "language"))
	if err != nil {
		return nil, err
	}
	if !s.Capabilities().Formatting {
		return nil, e.unsupported(p, "textDocument/formatting")
	}

	tabSize := intVal(in, "tab_size")
	if tabSize == 0 {
		tabSize = 4
	}
	if tabSize < 1 {
		return nil, core.NewError(core.InvalidArgument, "tab_size must be positive")
	}
	insertSpaces := true
	if raw, ok := in["insert_spaces"]; ok {
		value, ok := raw.(bool)
		if !ok {
			return nil, core.NewError(core.InvalidArgument, "insert_spaces must be a boolean")
		}
		insertSpaces = value
	}

	params := map[string]any{
		"textDocument": protocol.TextDocumentIdentifier{URI: d.URI},
		"options": map[string]any{
			"tabSize":      tabSize,
			"insertSpaces": insertSpaces,
		},
	}
	callCtx, cancel := e.callContext(ctx)
	var edits []protocol.TextEdit
	err = s.Request(callCtx, "textDocument/formatting", params, &edits)
	cancel()
	if err != nil {
		return nil, err
	}

	workspaceEdit := protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{d.URI: edits}}
	plan, files, editCount, err := e.workspaceEditPlan(workspaceEdit, s.Capabilities().PositionEncoding)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"applied":    false,
		"file_count": len(files),
		"edit_count": editCount,
		"files":      files,
	}
	apply, _ := in["apply"].(bool)
	if !apply {
		return result, nil
	}
	warnings, err := e.applyWorkspaceEditPlan(ctx, plan)
	if err != nil {
		return nil, err
	}
	result["applied"] = true
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return result, nil
}
