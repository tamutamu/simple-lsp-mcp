package tools

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
)

const (
	defaultSymbolSourceLines = 120
	maxSymbolSourceLines     = 500
	maxSymbolSourceBytes     = 16 * 1024
	signatureMaxLines        = 12
)

// InspectSymbol returns exactly the information needed to inspect a semantic
// declaration. It never reads outside the workspace, changes files, or silently
// claims that truncated source contains the whole declaration.
func (e *Engine) InspectSymbol(ctx context.Context, in map[string]any) (map[string]any, error) {
	view := stringVal(in, "view")
	if view == "" {
		view = "source"
	}
	if view != "source" && view != "signature" && view != "location" {
		return nil, core.NewError(core.InvalidArgument, "view must be source, signature or location")
	}
	maxLines := intVal(in, "max_lines")
	if maxLines == 0 {
		maxLines = defaultSymbolSourceLines
	}
	if maxLines < 1 || maxLines > maxSymbolSourceLines {
		return nil, core.NewError(core.InvalidArgument, fmt.Sprintf("max_lines must be between 1 and %d", maxSymbolSourceLines))
	}
	if err := targetOf(in).Validate(); err != nil {
		return nil, err
	}

	p, s, d, pos, err := e.target(ctx, in)
	if err != nil {
		return nil, err
	}

	var selected symbolNode
	path := relativeMust(e, d.Path)
	if id := stringVal(in, "symbol_id"); id != "" {
		// e.target has already validated this handle against a fresh file hash.
		record, err := e.Symbols.Get(id)
		if err != nil {
			return nil, err
		}
		selected = symbolNode{
			Name: record.Name, Kind: record.Kind, Range: record.Range,
			SelectionRange: record.SelectionRange, SymbolPath: record.SymbolPath,
		}
	} else {
		// Unlike regex matching, the LSP's documentSymbol tree distinguishes
		// nested names. symbol_path resolves to a declaration selection position;
		// path+line+column can name a declaration or its containing scope.
		_, _, roots, err := e.documentNodes(ctx, p, path)
		if err != nil {
			return nil, err
		}
		point, err := document.FromLSP(d.Text, pos, s.Capabilities().PositionEncoding)
		if err != nil {
			return nil, err
		}
		selected, err = findInspectNode(roots, point, stringVal(in, "symbol_path") != "")
		if err != nil {
			return nil, err
		}
	}

	result := map[string]any{
		"view": view, "path": path, "name": selected.Name,
		"kind": selected.Kind, "range": selected.Range,
	}
	if view == "location" {
		return result, nil
	}

	lines, truncated, err := extractSymbolSource(d.Text, selected.Range, maxLines, maxSymbolSourceBytes, view == "signature")
	if err != nil {
		return nil, err
	}
	result["code"] = lines
	result["meta"] = core.Meta{Complete: !truncated, Truncated: truncated}
	return result, nil
}

// findInspectNode prefers an exact LSP selection hit. Otherwise a source
// position chooses the smallest enclosing code scope, not a random local
// variable or a same-named declaration elsewhere.
func findInspectNode(nodes []symbolNode, position core.Position, exactOnly bool) (symbolNode, error) {
	var exact, enclosing symbolNode
	exactFound, enclosingFound := false, false
	var walk func([]symbolNode)
	walk = func(items []symbolNode) {
		for _, node := range items {
			if node.SelectionRange.Start == position {
				exact, exactFound = node, true
			}
			if !exactOnly && isStructuralSymbol(node.Kind) && inspectRangeContains(node.Range, position) {
				enclosing, enclosingFound = node, true
			}
			walk(node.Children)
		}
	}
	walk(nodes)
	if exactFound {
		return exact, nil
	}
	if enclosingFound {
		return enclosing, nil
	}
	return symbolNode{}, core.NewError(core.SymbolNotFound, "no LSP declaration at or enclosing the requested position")
}

func inspectRangeContains(r core.Range, p core.Position) bool {
	if r.Start.Line < 1 || r.End.Line < r.Start.Line {
		return false
	}
	beforeEnd := p.Line < r.End.Line || p.Line == r.End.Line && p.Column < r.End.Column
	afterStart := p.Line > r.Start.Line || p.Line == r.Start.Line && p.Column >= r.Start.Column
	return beforeEnd && afterStart
}

// extractSymbolSource returns the full *source lines* covering the declaration,
// preserving indentation and line endings. LSP ranges commonly begin at the
// first identifier rather than column 1; returning whole lines makes the
// snippet directly useful to a coding agent. No heuristic text searching is used.
func extractSymbolSource(text []byte, r core.Range, maxLines, maxBytes int, signature bool) (string, bool, error) {
	all := strings.SplitAfter(string(text), "\n")
	if r.Start.Line < 1 || r.End.Line < r.Start.Line || r.Start.Line > len(all) || r.End.Line > len(all) {
		return "", false, core.NewError(core.InvalidArgument, "LSP symbol range outside source file")
	}
	// LSP end is exclusive. If it points to column 1 of the next line, that
	// next line contains no declaration source.
	endLine := r.End.Line
	if r.End.Column == 1 && endLine > r.Start.Line {
		endLine--
	}
	availableEnd := endLine
	if availableEnd-r.Start.Line+1 > maxLines {
		availableEnd = r.Start.Line + maxLines - 1
	}
	if signature && availableEnd-r.Start.Line+1 > signatureMaxLines {
		availableEnd = r.Start.Line + signatureMaxLines - 1
	}
	if signature {
		// Stop after the declaration's opening boundary rather than returning
		// implementation statements. A multiline Python signature ends in
		// "):"; Go/TypeScript braces open the body. No code-body parsing is
		// claimed; this is a bounded declaration-header preview.
		for i := r.Start.Line - 1; i < availableEnd; i++ {
			line := strings.TrimSpace(all[i])
			if strings.HasSuffix(line, "):") || strings.HasSuffix(line, ":") ||
				strings.HasSuffix(line, "{") || strings.Contains(line, "=> {") {
				availableEnd = i + 1
				break
			}
		}
	}
	var sb strings.Builder
	lastLine := r.Start.Line - 1
	truncated := availableEnd < endLine
	for i := r.Start.Line - 1; i < availableEnd; i++ {
		line := all[i]
		if sb.Len()+len(line) > maxBytes {
			remaining := maxBytes - sb.Len()
			if remaining > 0 {
				// Cut at a UTF-8 boundary; don't generate invalid source text.
				part := line[:remaining]
				for !utf8.ValidString(part) {
					part = part[:len(part)-1]
				}
				sb.WriteString(part)
			}
			truncated = true
			lastLine = i + 1
			break
		}
		sb.WriteString(line)
		lastLine = i + 1
	}
	if lastLine < r.Start.Line {
		return "", false, core.NewError(core.InternalError, "empty declaration source range")
	}
	if signature && availableEnd < endLine {
		truncated = true
	}
	return sb.String(), truncated, nil
}
