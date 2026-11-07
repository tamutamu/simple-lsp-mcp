package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

// PreviewRename is deliberately a separate read-only entry point. Some
// noninteractive MCP clients refuse tools annotated as destructive even when
// the caller passes apply=false. Keep the existing RenameSymbol API for
// compatibility, but never allow a preview invocation to write.
func (e *Engine) PreviewRename(ctx context.Context, in map[string]any) (map[string]any, error) {
	detail := stringVal(in, "preview_detail")
	if detail != "" && detail != "summary" && detail != "full" {
		return nil, core.NewError(core.InvalidArgument, "preview_detail must be summary or full")
	}
	preview := make(map[string]any, len(in)+1)
	for key, value := range in {
		preview[key] = value
	}
	preview["apply"] = false
	result, err := e.RenameSymbol(ctx, preview)
	if err != nil {
		return nil, err
	}
	if detail == "full" {
		return result, nil
	}
	return compactRenamePreview(result), nil
}

// A project-wide semantic rename may update hundreds of locations. Repeating
// each replacement string and complete range in the MCP response is usually
// wasted model context when the agent first wants to know which files change.
// All edits have already passed the exact workspaceEditPlan validation.
// Keep the complete preview available via preview_detail=full for deliberate auditing.
// This only compacts preview_rename; rename_symbol remains backward compatible.
func compactRenamePreview(result map[string]any) map[string]any {
	files, ok := result["files"].([]fileEditPreview)
	if !ok {
		return result
	}
	const inlineEditLimit = 12
	const samplePerFile = 2
	const maxTotalSamples = 12
	totalEdits := 0
	for _, file := range files {
		totalEdits += len(file.Edits)
	}
	if totalEdits <= inlineEditLimit {
		return result
	}
	summaries := make([]map[string]any, 0, len(files))
	sampled := 0
	for _, file := range files {
		entry := map[string]any{
			"path":       file.Path,
			"edit_count": len(file.Edits),
		}
		if len(file.Edits) > 0 {
			entry["first_line"] = file.Edits[0].Range.Start.Line
			entry["last_line"] = file.Edits[len(file.Edits)-1].Range.Start.Line
		}
		budget := samplePerFile
		if remaining := maxTotalSamples - sampled; budget > remaining {
			budget = remaining
		}
		if budget > len(file.Edits) {
			budget = len(file.Edits)
		}
		if budget > 0 {
			entry["sample_edits"] = file.Edits[:budget]
			sampled += budget
		}
		if omitted := len(file.Edits) - budget; omitted > 0 {
			entry["omitted_edits"] = omitted
		}
		summaries = append(summaries, entry)
	}
	result["files"] = summaries
	result["preview_detail"] = "summary"
	result["preview_truncated"] = true
	result["sample_edit_count"] = sampled
	result["full_preview_available"] = "preview_rename(preview_detail=full)"
	return result
}

// CompactAppliedRename retains the result needed after a verified write,
// without echoing every replacement already applied to the filesystem.
// The full plan remains available through RenameSymbol and preview_rename.
func CompactAppliedRename(result map[string]any) map[string]any {
	if applied, _ := result["applied"].(bool); !applied {
		return result
	}
	files, ok := result["files"].([]fileEditPreview)
	if !ok {
		return result
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	summary := map[string]any{
		"applied":       true,
		"new_name":      result["new_name"],
		"file_count":    result["file_count"],
		"edit_count":    result["edit_count"],
		"changed_files": paths,
	}
	if warnings, present := result["warnings"]; present {
		summary["warnings"] = warnings
	}
	return summary
}

// RenameSymbol asks the selected language server to compute a semantic rename.
// It is preview-only unless apply=true. The server never computes replacements
// itself; it only validates and optionally applies the returned WorkspaceEdit.
func (e *Engine) RenameSymbol(ctx context.Context, in map[string]any) (map[string]any, error) {
	newName := stringVal(in, "new_name")
	if strings.TrimSpace(newName) == "" {
		return nil, core.NewError(core.InvalidArgument, "new_name must be a non-empty string")
	}
	p, s, d, pos, err := e.target(ctx, in)
	if err != nil {
		return nil, err
	}
	caps := s.Capabilities()
	if !caps.Rename {
		return nil, e.unsupported(p, "textDocument/rename")
	}

	positionParams := map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}, "position": pos}
	var preparation map[string]any
	if caps.PrepareRename {
		callCtx, cancel := e.callContext(ctx)
		var raw json.RawMessage
		err := s.Request(callCtx, "textDocument/prepareRename", positionParams, &raw)
		cancel()
		if err != nil {
			return nil, err
		}
		preparation, err = renamePreparation(d.Text, raw, caps.PositionEncoding)
		if err != nil {
			return nil, err
		}
	}

	// tsserver and Pyright may not include unopened project files in rename
	// immediately after the first didOpen. Warm only source files that contain
	// the old symbol spelling; the language server still computes every edit.
	// The filesystem scan is bounded and fails closed instead of accepting a
	// potentially partial semantic rename.
	if p.SessionKey == "typescript-javascript" || p.SessionKey == "python" {
		// prepareRename is optional in LSP. With only path/line/column,
		// name-based candidate warm-up previously became a no-op when the
		// server omitted that capability, risking incomplete cross-file edits.
		// This spelling only selects files to open; textDocument/rename
		// remains the sole authority for semantic edit locations.
		name := e.renameWarmCandidateName(in, d.Text, preparation, pos, caps.PositionEncoding)
		if name != "" {
			if err := e.warmProjectSymbolCandidates(ctx, p, s, name); err != nil {
				return nil, err
			}
			if err := e.primeProjectSemantics(ctx, p, s, d, pos); err != nil {
				return nil, err
			}
		}
	}

	callCtx, cancel := e.callContext(ctx)
	var edit protocol.WorkspaceEdit
	err = s.Request(callCtx, "textDocument/rename", map[string]any{
		"textDocument": protocol.TextDocumentIdentifier{URI: d.URI},
		"position":     pos,
		"newName":      newName,
	}, &edit)
	cancel()
	if err != nil {
		return nil, err
	}
	plan, files, editCount, err := e.workspaceEditPlan(edit, caps.PositionEncoding)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"new_name": newName, "applied": false, "file_count": len(files), "edit_count": editCount, "files": files}
	if preparation != nil {
		result["prepare"] = preparation
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

func (e *Engine) renameWarmCandidateName(in map[string]any, text []byte, preparation map[string]any, position protocol.Position, encoding string) string {
	name := e.renameWarmName(in, text, preparation)
	if name != "" {
		return name
	}
	return warmIdentifierAtPosition(text, position, encoding)
}

func (e *Engine) renameWarmName(in map[string]any, text []byte, preparation map[string]any) string {
	if preparation != nil {
		if placeholder, _ := preparation["placeholder"].(string); placeholder != "" {
			return placeholder
		}
		if r, ok := preparation["range"].(core.Range); ok {
			start, startErr := byteOffsetAtCorePosition(text, r.Start)
			end, endErr := byteOffsetAtCorePosition(text, r.End)
			if startErr == nil && endErr == nil && start < end && end <= len(text) {
				return string(text[start:end])
			}
		}
	}
	return e.targetWarmName(in)
}

func renamePreparation(text []byte, raw json.RawMessage, encoding string) (map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, core.NewError(core.InvalidArgument, "language server does not allow rename at the target")
	}
	var direct protocol.Range
	if json.Unmarshal(trimmed, &direct) == nil {
		var probe map[string]json.RawMessage
		if json.Unmarshal(trimmed, &probe) == nil && probe["start"] != nil && probe["end"] != nil {
			converted, err := rangeFromText(text, direct, encoding)
			if err != nil {
				return nil, err
			}
			return map[string]any{"range": converted}, nil
		}
	}
	var options struct {
		Range           *protocol.Range `json:"range,omitempty"`
		Placeholder     string          `json:"placeholder,omitempty"`
		DefaultBehavior bool            `json:"defaultBehavior,omitempty"`
	}
	if err := json.Unmarshal(trimmed, &options); err != nil {
		return nil, core.WithCause(core.InternalError, "invalid prepareRename response", err)
	}
	out := map[string]any{}
	if options.Range != nil {
		converted, err := rangeFromText(text, *options.Range, encoding)
		if err != nil {
			return nil, err
		}
		out["range"] = converted
	}
	if options.Placeholder != "" {
		out["placeholder"] = options.Placeholder
	}
	if options.DefaultBehavior {
		out["default_behavior"] = true
	}
	if len(out) == 0 {
		return nil, core.NewError(core.InvalidArgument, "language server returned an unusable prepareRename response")
	}
	return out, nil
}
