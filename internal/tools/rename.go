package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
)

type renameEditPreview struct {
	Range   core.Range `json:"range"`
	NewText string     `json:"new_text"`
}

type renameFilePreview struct {
	Path  string              `json:"path"`
	Edits []renameEditPreview `json:"edits"`
}

type plannedRenameFile struct {
	path     string
	full     string
	mode     fs.FileMode
	original []byte
	updated  []byte
	edits    []renameEditPreview
}

type computedRenameEdit struct {
	start   int
	end     int
	preview renameEditPreview
}

// RenameSymbol asks the selected language server to compute a semantic rename.
// It is preview-only unless apply=true. The MCP server never computes rename
// replacements itself; it only validates and optionally applies WorkspaceEdit.
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

	params := map[string]any{
		"textDocument": protocol.TextDocumentIdentifier{URI: d.URI},
		"position":     pos,
	}
	var preparation map[string]any
	if caps.PrepareRename {
		callCtx, cancel := e.callContext(ctx)
		var raw json.RawMessage
		err := s.Request(callCtx, "textDocument/prepareRename", params, &raw)
		cancel()
		if err != nil {
			return nil, err
		}
		preparation, err = renamePreparation(d.Text, raw, caps.PositionEncoding)
		if err != nil {
			return nil, err
		}
	}

	renameParams := map[string]any{
		"textDocument": protocol.TextDocumentIdentifier{URI: d.URI},
		"position":     pos,
		"newName":      newName,
	}
	callCtx, cancel := e.callContext(ctx)
	var workspaceEdit protocol.WorkspaceEdit
	err = s.Request(callCtx, "textDocument/rename", renameParams, &workspaceEdit)
	cancel()
	if err != nil {
		return nil, err
	}

	plan, files, editCount, err := e.workspaceEditPlan(workspaceEdit, caps.PositionEncoding)
	if err != nil {
		return nil, err
	}
	result := map[string]any{
		"new_name":   newName,
		"applied":    false,
		"file_count": len(files),
		"edit_count": editCount,
		"files":      files,
	}
	if preparation != nil {
		result["prepare"] = preparation
	}
	apply, _ := in["apply"].(bool)
	if !apply {
		return result, nil
	}

	warnings, err := e.applyRenamePlan(ctx, plan)
	if err != nil {
		return nil, err
	}
	result["applied"] = true
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return result, nil
}

func renamePreparation(text []byte, raw json.RawMessage, encoding string) (map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, core.NewError(core.InvalidArgument, "language server does not allow rename at the target")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return nil, core.WithCause(core.InternalError, "invalid prepareRename response", err)
	}
	if _, directRange := object["start"]; directRange {
		var r protocol.Range
		if err := json.Unmarshal(trimmed, &r); err != nil {
			return nil, core.WithCause(core.InternalError, "invalid prepareRename range", err)
		}
		converted, err := rangeFromText(text, r, encoding)
		if err != nil {
			return nil, err
		}
		return map[string]any{"range": converted}, nil
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

func (e *Engine) workspaceEditPlan(edit protocol.WorkspaceEdit, encoding string) ([]plannedRenameFile, []renameFilePreview, int, error) {
	byPath := map[string][]protocol.TextEdit{}
	add := func(uri string, edits []protocol.TextEdit) error {
		path, err := normalize.URIPath(e.WS, uri)
		if err != nil {
			return core.WithCause(core.InvalidPath, "rename edit escapes the workspace", err)
		}
		byPath[path] = append(byPath[path], edits...)
		return nil
	}
	for uri, edits := range edit.Changes {
		if err := add(uri, edits); err != nil {
			return nil, nil, 0, err
		}
	}
	for _, raw := range edit.DocumentChanges {
		var envelope struct {
			Kind string `json:"kind,omitempty"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, nil, 0, core.WithCause(core.InternalError, "invalid documentChanges entry", err)
		}
		if envelope.Kind != "" {
			return nil, nil, 0, core.NewError(core.InvalidArgument, "rename WorkspaceEdit contains unsupported resource operation "+envelope.Kind)
		}
		var documentEdit protocol.TextDocumentEdit
		if err := json.Unmarshal(raw, &documentEdit); err != nil || documentEdit.TextDocument.URI == "" {
			return nil, nil, 0, core.NewError(core.InternalError, "rename WorkspaceEdit contains an invalid text document edit")
		}
		if err := add(documentEdit.TextDocument.URI, documentEdit.Edits); err != nil {
			return nil, nil, 0, err
		}
	}

	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	plan := make([]plannedRenameFile, 0, len(paths))
	preview := make([]renameFilePreview, 0, len(paths))
	editCount := 0
	for _, path := range paths {
		lexical := filepath.Join(e.WS.Root(), filepath.FromSlash(path))
		info, err := os.Lstat(lexical)
		if err != nil {
			return nil, nil, 0, core.WithCause(core.InvalidPath, "rename target does not exist: "+path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, nil, 0, core.NewError(core.InvalidPath, "rename edits may target regular workspace files only: "+path)
		}
		full, err := e.WS.Resolve(path)
		if err != nil {
			return nil, nil, 0, err
		}
		original, err := os.ReadFile(full)
		if err != nil {
			return nil, nil, 0, err
		}
		if !utf8.Valid(original) {
			return nil, nil, 0, core.NewError(core.InvalidArgument, "rename target is not valid UTF-8: "+path)
		}

		computed := make([]computedRenameEdit, 0, len(byPath[path]))
		for _, textEdit := range byPath[path] {
			r, err := rangeFromText(original, textEdit.Range, encoding)
			if err != nil {
				return nil, nil, 0, core.WithCause(core.InvalidArgument, "rename edit has an invalid range in "+path, err)
			}
			start, err := byteOffsetAtCorePosition(original, r.Start)
			if err != nil {
				return nil, nil, 0, err
			}
			end, err := byteOffsetAtCorePosition(original, r.End)
			if err != nil {
				return nil, nil, 0, err
			}
			if end < start {
				return nil, nil, 0, core.NewError(core.InvalidArgument, "rename edit range is reversed in "+path)
			}
			computed = append(computed, computedRenameEdit{
				start: start,
				end:   end,
				preview: renameEditPreview{
					Range:   r,
					NewText: textEdit.NewText,
				},
			})
		}
		sort.Slice(computed, func(i, j int) bool {
			if computed[i].start != computed[j].start {
				return computed[i].start < computed[j].start
			}
			return computed[i].end < computed[j].end
		})
		for i := 1; i < len(computed); i++ {
			prev, curr := computed[i-1], computed[i]
			if curr.start < prev.end || (curr.start == prev.start && (curr.start == curr.end || prev.start == prev.end)) {
				return nil, nil, 0, core.NewError(core.InvalidArgument, "rename WorkspaceEdit contains overlapping edits in "+path)
			}
		}

		updated := append([]byte(nil), original...)
		for i := len(computed) - 1; i >= 0; i-- {
			edit := computed[i]
			next := make([]byte, 0, len(updated)-(edit.end-edit.start)+len(edit.preview.NewText))
			next = append(next, updated[:edit.start]...)
			next = append(next, edit.preview.NewText...)
			next = append(next, updated[edit.end:]...)
			updated = next
		}
		edits := make([]renameEditPreview, len(computed))
		for i, edit := range computed {
			edits[i] = edit.preview
		}
		plan = append(plan, plannedRenameFile{
			path: path, full: full, mode: info.Mode(), original: original, updated: updated, edits: edits,
		})
		preview = append(preview, renameFilePreview{Path: path, Edits: edits})
		editCount += len(edits)
	}
	return plan, preview, editCount, nil
}

func (e *Engine) applyRenamePlan(ctx context.Context, plan []plannedRenameFile) ([]string, error) {
	written := make([]plannedRenameFile, 0, len(plan))
	for _, file := range plan {
		if err := os.WriteFile(file.full, file.updated, file.mode.Perm()); err != nil {
			rollbackFailures := make([]string, 0)
			for i := len(written) - 1; i >= 0; i-- {
				if rollbackErr := os.WriteFile(written[i].full, written[i].original, written[i].mode.Perm()); rollbackErr != nil {
					rollbackFailures = append(rollbackFailures, written[i].path+": "+rollbackErr.Error())
				}
			}
			message := "failed to apply rename to " + file.path
			if len(rollbackFailures) > 0 {
				message += "; rollback also failed for " + strings.Join(rollbackFailures, ", ")
			}
			return nil, core.WithCause(core.InternalError, message, err)
		}
		written = append(written, file)
	}

	warnings := make([]string, 0)
	for _, file := range plan {
		p, err := language.FromPath(file.path)
		if err != nil {
			warnings = append(warnings, "could not infer language after editing "+file.path)
			continue
		}
		s, err := e.Sessions.ForPath(p.SessionKey, file.path)
		if err != nil {
			warnings = append(warnings, "could not select language server after editing "+file.path+": "+err.Error())
			continue
		}
		syncCtx, cancel := e.callContext(ctx)
		_, err = e.Docs.Sync(syncCtx, s, file.full, p.LanguageID)
		cancel()
		if err != nil {
			warnings = append(warnings, "file was written but LSP resync failed for "+file.path+": "+err.Error())
		}
	}
	return warnings, nil
}

func byteOffsetAtCorePosition(text []byte, p core.Position) (int, error) {
	if p.Line < 1 || p.Column < 1 {
		return 0, core.NewError(core.InvalidArgument, "rename edit position must be one-based")
	}
	line, start := 1, 0
	for i, b := range text {
		if line == p.Line {
			break
		}
		if b == '\n' {
			line++
			start = i + 1
		}
	}
	if line != p.Line {
		return 0, core.NewError(core.InvalidArgument, "rename edit line is out of range")
	}
	end := len(text)
	if i := bytes.IndexByte(text[start:], '\n'); i >= 0 {
		end = start + i
	}
	if end > start && text[end-1] == '\r' {
		end--
	}
	target := p.Column - 1
	offset := start
	for cp := 0; cp < target; cp++ {
		if offset >= end {
			return 0, core.NewError(core.InvalidArgument, "rename edit column is out of range")
		}
		_, size := utf8.DecodeRune(text[offset:end])
		if size == 0 {
			return 0, core.NewError(core.InvalidArgument, "rename edit column is invalid")
		}
		offset += size
	}
	return offset, nil
}

var _ = document.Document{}
