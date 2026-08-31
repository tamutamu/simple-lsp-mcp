package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

const hoverMaxBytes = 64 << 10

// Hover returns the language server's inferred type and documentation for
// an arbitrary position, independent of any named, declared symbol. It
// exists for the one thing no other tool can answer: the type a language
// server infers for an expression that has no explicit declaration of its
// own, such as a local variable assigned from a generic call. Prefer
// get_symbol or get_symbol_context for anything that has a name.
func (e *Engine) Hover(ctx context.Context, in map[string]any) (map[string]any, error) {
	p, s, d, pos, err := e.target(ctx, in)
	if err != nil {
		return nil, err
	}
	if !capable(s.Capabilities(), "hover") {
		return nil, e.unsupported(p, "textDocument/hover")
	}
	var raw json.RawMessage
	callCtx, cancel := e.callContext(ctx)
	err = s.Request(callCtx, "textDocument/hover", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}, "position": pos}, &raw)
	cancel()
	if err != nil {
		return nil, err
	}
	if isNullJSON(raw) {
		return map[string]any{"hover": nil, "meta": core.Meta{Complete: true}}, nil
	}
	var h protocol.Hover
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, err
	}
	text, kind := hoverText(h.Contents)
	if text == "" {
		return map[string]any{"hover": nil, "meta": core.Meta{Complete: true}}, nil
	}
	truncated := false
	if len(text) > hoverMaxBytes {
		text = text[:hoverMaxBytes]
		truncated = true
	}
	path := relativeMust(e, d.Path)
	out := map[string]any{"path": path, "kind": kind, "contents": text}
	if h.Range != nil {
		if r, err := e.rangeForPath(path, *h.Range, s.Capabilities().PositionEncoding); err == nil {
			out["range"] = r
		}
	}
	return map[string]any{"hover": out, "meta": core.Meta{Complete: true, SourceTruncated: truncated}}, nil
}

// isNullJSON reports whether an LSP result was absent or explicitly null.
// A JSON-RPC "result": null response reaches here as the four literal
// bytes "null", not as an empty slice, so both must be checked.
func isNullJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || string(trimmed) == "null"
}

// hoverText flattens the three shapes LSP permits for Hover.Contents
// (MarkupContent, MarkedString, or MarkedString[]) into one string, and
// reports the markup kind of the result ("markdown" or "plaintext").
// The shape is decided by the first non-space byte rather than by trying
// each Unmarshal in turn: MarkupContent and MarkedString are both JSON
// objects, so attempting them in sequence would misclassify one as the
// other whenever the unused fields happen to be absent.
func hoverText(raw json.RawMessage) (text, kind string) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", ""
	}
	switch trimmed[0] {
	case 'n': // null
		return "", ""
	case '[':
		var xs []json.RawMessage
		if json.Unmarshal(trimmed, &xs) != nil {
			return "", ""
		}
		parts := make([]string, 0, len(xs))
		for _, x := range xs {
			if s := markedString(x); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n\n"), "markdown"
	case '"':
		var s string
		if json.Unmarshal(trimmed, &s) != nil {
			return "", ""
		}
		return s, "markdown"
	case '{':
		var m struct {
			Kind     string `json:"kind"`
			Language string `json:"language"`
			Value    string `json:"value"`
		}
		if json.Unmarshal(trimmed, &m) != nil {
			return "", ""
		}
		if m.Kind != "" {
			return m.Value, m.Kind
		}
		return markedString(trimmed), "markdown"
	}
	return "", ""
}

// markedString renders one MarkedString element, or a bare string, as markdown.
func markedString(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	if trimmed[0] == '"' {
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			return s
		}
		return ""
	}
	var m protocol.MarkedString
	if json.Unmarshal(trimmed, &m) != nil {
		return ""
	}
	if m.Language == "" {
		return m.Value
	}
	return "```" + m.Language + "\n" + m.Value + "\n```"
}
