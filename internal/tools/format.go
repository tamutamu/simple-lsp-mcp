package tools

import (
	"context"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

// FormatDocument previews or applies LSP formatting. Supplying a complete
// start/end range switches to textDocument/rangeFormatting.
func (e *Engine) FormatDocument(ctx context.Context, in map[string]any) (map[string]any, error) {
	path := stringVal(in, "path")
	if path == "" {
		return nil, core.NewError(core.InvalidArgument, "path is required")
	}
	p, s, d, err := e.document(ctx, path, stringVal(in, "language"))
	if err != nil {
		return nil, err
	}
	caps := s.Capabilities()

	tabSize := intVal(in, "tab_size")
	if tabSize == 0 {
		tabSize = 4
	}
	if tabSize < 1 {
		return nil, core.NewError(core.InvalidArgument, "tab_size must be positive")
	}
	insertSpaces := true
	if value, ok := in["insert_spaces"].(bool); ok {
		insertSpaces = value
	}
	options := map[string]any{"tabSize": tabSize, "insertSpaces": insertSpaces}
	for _, pair := range []struct{ input, lsp string }{
		{"trim_trailing_whitespace", "trimTrailingWhitespace"},
		{"insert_final_newline", "insertFinalNewline"},
		{"trim_final_newlines", "trimFinalNewlines"},
	} {
		if value, ok := in[pair.input].(bool); ok {
			options[pair.lsp] = value
		}
	}

	method := "textDocument/formatting"
	params := map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}, "options": options}
	line, column := intVal(in, "line"), intVal(in, "column")
	endLine, endColumn := intVal(in, "end_line"), intVal(in, "end_column")
	anyRange := line != 0 || column != 0 || endLine != 0 || endColumn != 0
	if anyRange {
		if line < 1 || column < 1 || endLine < 1 || endColumn < 1 {
			return nil, core.NewError(core.InvalidArgument, "line, column, end_line, and end_column must all be specified for range formatting")
		}
		if !caps.RangeFormatting {
			return nil, e.unsupported(p, "textDocument/rangeFormatting")
		}
		start, err := document.ToLSP(d.Text, core.Position{Line: line, Column: column}, caps.PositionEncoding)
		if err != nil {
			return nil, err
		}
		end, err := document.ToLSP(d.Text, core.Position{Line: endLine, Column: endColumn}, caps.PositionEncoding)
		if err != nil {
			return nil, err
		}
		method = "textDocument/rangeFormatting"
		params["range"] = protocol.Range{Start: start, End: end}
	} else if !caps.Formatting {
		return nil, e.unsupported(p, "textDocument/formatting")
	}

	callCtx, cancel := e.callContext(ctx)
	var edits []protocol.TextEdit
	err = s.Request(callCtx, method, params, &edits)
	cancel()
	if err != nil {
		return nil, err
	}
	workspaceEdit := protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{d.URI: edits}}
	plan, files, editCount, err := e.workspaceEditPlan(workspaceEdit, caps.PositionEncoding)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"applied": false, "file_count": len(files), "edit_count": editCount, "files": files, "method": method}
	apply, _ := in["apply"].(bool)
	if !apply || editCount == 0 {
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
