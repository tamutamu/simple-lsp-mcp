package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
)

type codeActionRequestResult struct {
	Profile  language.Profile
	Session  *session.Session
	Document document.Document
	Actions  []json.RawMessage
}

type codeActionSummary struct {
	Index        int    `json:"index"`
	ActionID     string `json:"action_id"`
	Title        string `json:"title"`
	Kind         string `json:"kind,omitempty"`
	Preferred    bool   `json:"preferred,omitempty"`
	Disabled     string `json:"disabled,omitempty"`
	HasEdit      bool   `json:"has_edit"`
	HasCommand   bool   `json:"has_command"`
	NeedsResolve bool   `json:"needs_resolve,omitempty"`
}

func (e *Engine) codeActions(ctx context.Context, in map[string]any) (codeActionRequestResult, error) {
	path := stringVal(in, "path")
	if path == "" {
		return codeActionRequestResult{}, core.NewError(core.InvalidArgument, "path is required")
	}
	p, s, d, err := e.document(ctx, path, stringVal(in, "language"))
	if err != nil {
		return codeActionRequestResult{}, err
	}
	caps := s.Capabilities()
	if !caps.CodeAction {
		return codeActionRequestResult{}, e.unsupported(p, "textDocument/codeAction")
	}
	line, column := intVal(in, "line"), intVal(in, "column")
	if line < 1 || column < 1 {
		return codeActionRequestResult{}, core.NewError(core.InvalidArgument, "line and column are required and one-based")
	}
	endLine, endColumn := intVal(in, "end_line"), intVal(in, "end_column")
	if endLine == 0 {
		endLine = line
	}
	if endColumn == 0 {
		endColumn = column
	}
	start, err := document.ToLSP(d.Text, core.Position{Line: line, Column: column}, caps.PositionEncoding)
	if err != nil {
		return codeActionRequestResult{}, err
	}
	end, err := document.ToLSP(d.Text, core.Position{Line: endLine, Column: endColumn}, caps.PositionEncoding)
	if err != nil {
		return codeActionRequestResult{}, err
	}
	diagnostics := s.Diagnostics(d.URI)
	if caps.Diagnostics {
		var report struct {
			Items []protocol.Diagnostic `json:"items"`
		}
		callCtx, cancel := e.callContext(ctx)
		diagnosticErr := s.Request(callCtx, "textDocument/diagnostic", map[string]any{
			"textDocument":     protocol.TextDocumentIdentifier{URI: d.URI},
			"previousResultId": nil,
		}, &report)
		cancel()
		if diagnosticErr == nil {
			diagnostics = report.Items
		}
	} else if len(diagnostics) == 0 && e.DiagnosticsWait > 0 {
		timer := time.NewTimer(e.DiagnosticsWait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return codeActionRequestResult{}, ctx.Err()
		case <-timer.C:
			diagnostics = s.Diagnostics(d.URI)
		}
	}
	contextValue := map[string]any{"diagnostics": diagnostics}
	var only []string
	switch kinds := in["code_action_kinds"].(type) {
	case []any:
		for _, raw := range kinds {
			if value, ok := raw.(string); ok && value != "" {
				only = append(only, value)
			}
		}
	case []string:
		for _, value := range kinds {
			if value != "" {
				only = append(only, value)
			}
		}
	}
	if len(only) > 0 {
		contextValue["only"] = only
	}
	callCtx, cancel := e.callContext(ctx)
	var actions []json.RawMessage
	err = s.Request(callCtx, "textDocument/codeAction", map[string]any{
		"textDocument": protocol.TextDocumentIdentifier{URI: d.URI},
		"range":        protocol.Range{Start: start, End: end},
		"context":      contextValue,
	}, &actions)
	cancel()
	if err != nil {
		return codeActionRequestResult{}, err
	}
	return codeActionRequestResult{Profile: p, Session: s, Document: d, Actions: actions}, nil
}

// GetCodeActions lists LSP code actions without executing commands or writing.
func (e *Engine) GetCodeActions(ctx context.Context, in map[string]any) (map[string]any, error) {
	result, err := e.codeActions(ctx, in)
	if err != nil {
		return nil, err
	}
	limit, err := e.limit(in)
	if err != nil {
		return nil, err
	}
	summaries := make([]codeActionSummary, 0, len(result.Actions))
	for i, raw := range result.Actions {
		if len(summaries) >= limit {
			break
		}
		action, isAction := decodeCodeAction(raw)
		if !isAction {
			var command protocol.Command
			if json.Unmarshal(raw, &command) == nil && command.Command != "" {
				summaries = append(summaries, codeActionSummary{Index: i, ActionID: codeActionID(raw), Title: command.Title, HasCommand: true})
			}
			continue
		}
		summary := codeActionSummary{Index: i, ActionID: codeActionID(raw), Title: action.Title, Kind: action.Kind, Preferred: action.IsPreferred, HasEdit: action.Edit != nil, HasCommand: action.Command != nil}
		if action.Disabled != nil {
			summary.Disabled = action.Disabled.Reason
		}
		if action.Edit == nil && action.Data != nil && result.Session.Capabilities().CodeActionResolve {
			summary.NeedsResolve = true
		}
		summaries = append(summaries, summary)
	}
	return map[string]any{"actions": summaries, "meta": core.Meta{Complete: len(summaries) == len(result.Actions), Truncated: len(summaries) < len(result.Actions)}}, nil
}

// ApplyCodeAction re-queries actions at the same range and applies only the
// selected action's WorkspaceEdit. Commands are intentionally not executed.
func (e *Engine) ApplyCodeAction(ctx context.Context, in map[string]any) (map[string]any, error) {
	actionID := stringVal(in, "action_id")
	if actionID == "" {
		return nil, core.NewError(core.InvalidArgument, "action_id is required; call get_code_actions first")
	}
	result, err := e.codeActions(ctx, in)
	if err != nil {
		return nil, err
	}
	selected := -1
	for i, raw := range result.Actions {
		if codeActionID(raw) == actionID {
			selected = i
			break
		}
	}
	if selected < 0 {
		return nil, core.NewError(core.InvalidArgument, "code action changed or expired; call get_code_actions again")
	}
	action, ok := decodeCodeAction(result.Actions[selected])
	if !ok {
		return nil, core.NewError(core.MethodNotSupported, "selected item is a command, not a WorkspaceEdit-backed code action")
	}
	if action.Disabled != nil {
		return nil, core.NewError(core.InvalidArgument, "selected code action is disabled: "+action.Disabled.Reason)
	}
	if action.Edit == nil && action.Data != nil && result.Session.Capabilities().CodeActionResolve {
		callCtx, cancel := e.callContext(ctx)
		var resolved protocol.CodeAction
		err := result.Session.Request(callCtx, "codeAction/resolve", action, &resolved)
		cancel()
		if err != nil {
			return nil, err
		}
		action = resolved
	}
	if action.Command != nil {
		return nil, core.NewError(core.MethodNotSupported, "selected code action also requires workspace/executeCommand; partial application is intentionally refused")
	}
	if action.Edit == nil {
		return nil, core.NewError(core.MethodNotSupported, "selected code action has no WorkspaceEdit; command execution is intentionally unsupported")
	}
	plan, files, editCount, err := e.workspaceEditPlan(*action.Edit, result.Session.Capabilities().PositionEncoding)
	if err != nil {
		return nil, err
	}
	warnings, err := e.applyWorkspaceEditPlan(ctx, plan)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"action_id": actionID, "index": selected, "title": action.Title, "kind": action.Kind, "applied": true, "file_count": len(files), "edit_count": editCount, "files": files}
	if len(warnings) > 0 {
		out["warnings"] = warnings
	}
	return out, nil
}

func codeActionID(raw json.RawMessage) string {
	var value any
	canonical := []byte(raw)
	if json.Unmarshal(raw, &value) == nil {
		if encoded, err := json.Marshal(value); err == nil {
			canonical = encoded
		}
	}
	sum := sha256.Sum256(canonical)
	return "action_" + hex.EncodeToString(sum[:8])
}

func decodeCodeAction(raw json.RawMessage) (protocol.CodeAction, bool) {
	var action protocol.CodeAction
	if json.Unmarshal(raw, &action) != nil || action.Title == "" {
		return protocol.CodeAction{}, false
	}
	// A bare Command has title+command. CodeAction uses a nested command object
	// and usually kind/edit/data/diagnostics/preferred/disabled fields.
	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) != nil {
		return protocol.CodeAction{}, false
	}
	if _, bareCommand := probe["command"]; bareCommand && probe["kind"] == nil && probe["edit"] == nil && probe["data"] == nil && probe["diagnostics"] == nil && probe["isPreferred"] == nil && probe["disabled"] == nil {
		var commandString string
		if json.Unmarshal(probe["command"], &commandString) == nil && commandString != "" {
			return protocol.CodeAction{}, false
		}
	}
	return action, true
}
