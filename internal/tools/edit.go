package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
)

type editPreview struct {
	Range   core.Range `json:"range"`
	NewText string     `json:"new_text"`
}

type fileEditPreview struct {
	Path  string        `json:"path"`
	Edits []editPreview `json:"edits"`
}

type plannedEditFile struct {
	path     string
	full     string
	mode     fs.FileMode
	original []byte
	updated  []byte
	edits    []editPreview
}

type computedEdit struct {
	start   int
	end     int
	preview editPreview
}

// workspaceEditPlan validates the entire LSP WorkspaceEdit before any file is
// modified. Only text edits to existing regular files inside the workspace are
// accepted. Resource operations are deliberately rejected.
func (e *Engine) workspaceEditPlan(edit protocol.WorkspaceEdit, encoding string) ([]plannedEditFile, []fileEditPreview, int, error) {
	byPath := map[string][]protocol.TextEdit{}
	add := func(uri string, edits []protocol.TextEdit) error {
		path, err := normalize.URIPath(e.WS, uri)
		if err != nil {
			return core.WithCause(core.InvalidPath, "LSP edit escapes the workspace", err)
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
			return nil, nil, 0, core.NewError(core.InvalidArgument, "WorkspaceEdit contains unsupported resource operation "+envelope.Kind)
		}
		var documentEdit protocol.TextDocumentEdit
		if err := json.Unmarshal(raw, &documentEdit); err != nil || documentEdit.TextDocument.URI == "" {
			return nil, nil, 0, core.NewError(core.InternalError, "WorkspaceEdit contains an invalid text document edit")
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

	plan := make([]plannedEditFile, 0, len(paths))
	preview := make([]fileEditPreview, 0, len(paths))
	editCount := 0
	for _, path := range paths {
		lexical := filepath.Join(e.WS.Root(), filepath.FromSlash(path))
		info, err := os.Lstat(lexical)
		if err != nil {
			return nil, nil, 0, core.WithCause(core.InvalidPath, "edit target does not exist: "+path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, nil, 0, core.NewError(core.InvalidPath, "LSP edits may target regular workspace files only: "+path)
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
			return nil, nil, 0, core.NewError(core.InvalidArgument, "edit target is not valid UTF-8: "+path)
		}

		computed := make([]computedEdit, 0, len(byPath[path]))
		for _, textEdit := range byPath[path] {
			r, err := rangeFromText(original, textEdit.Range, encoding)
			if err != nil {
				return nil, nil, 0, core.WithCause(core.InvalidArgument, "LSP edit has an invalid range in "+path, err)
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
				return nil, nil, 0, core.NewError(core.InvalidArgument, "LSP edit range is reversed in "+path)
			}
			computed = append(computed, computedEdit{start: start, end: end, preview: editPreview{Range: r, NewText: textEdit.NewText}})
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
				return nil, nil, 0, core.NewError(core.InvalidArgument, "WorkspaceEdit contains overlapping edits in "+path)
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
		edits := make([]editPreview, len(computed))
		for i, edit := range computed {
			edits[i] = edit.preview
		}
		plan = append(plan, plannedEditFile{path: path, full: full, mode: info.Mode(), original: original, updated: updated, edits: edits})
		preview = append(preview, fileEditPreview{Path: path, Edits: edits})
		editCount += len(edits)
	}
	return plan, preview, editCount, nil
}

// applyWorkspaceEditPlan writes only after the complete edit was validated.
// On a later write failure it restores every file already written.
func (e *Engine) applyWorkspaceEditPlan(ctx context.Context, plan []plannedEditFile) ([]string, error) {
	// Re-check every target before the first write. A stale or replaced file
	// aborts the entire operation without leaving a partially-applied edit.
	for _, file := range plan {
		info, err := os.Lstat(file.full)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, core.NewError(core.InvalidPath, "edit target changed before apply: "+file.path)
		}
		current, err := os.ReadFile(file.full)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(current, file.original) {
			return nil, core.NewError(core.StaleSymbol, "edit target changed after the language server computed its WorkspaceEdit: "+file.path)
		}
	}

	written := make([]plannedEditFile, 0, len(plan))
	for _, file := range plan {
		if err := os.WriteFile(file.full, file.updated, file.mode.Perm()); err != nil {
			rollbackFailures := make([]string, 0)
			for i := len(written) - 1; i >= 0; i-- {
				if rollbackErr := os.WriteFile(written[i].full, written[i].original, written[i].mode.Perm()); rollbackErr != nil {
					rollbackFailures = append(rollbackFailures, written[i].path+": "+rollbackErr.Error())
				}
			}
			message := "failed to apply LSP edit to " + file.path
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
		return 0, core.NewError(core.InvalidArgument, "edit position must be one-based")
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
		return 0, core.NewError(core.InvalidArgument, "edit line is out of range")
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
			return 0, core.NewError(core.InvalidArgument, "edit column is out of range")
		}
		_, size := utf8.DecodeRune(text[offset:end])
		if size == 0 {
			return 0, core.NewError(core.InvalidArgument, "edit column is invalid")
		}
		offset += size
	}
	return offset, nil
}
