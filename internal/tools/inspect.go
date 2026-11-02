package tools

import (
	"bytes"
	"context"
	"encoding/json"
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

// InspectSymbol accepts one semantic target, or a small list of known targets.
// The latter reduces agent/model turns for edits affecting multiple functions
// while retaining exact LSP identity and per-target source boundaries.
func (e *Engine) InspectSymbol(ctx context.Context, in map[string]any) (map[string]any, error) {
	if items, present := in["targets"]; present {
		return e.inspectBatch(ctx, in, items)
	}
	return e.inspectSingle(ctx, in)
}

func (e *Engine) inspectBatch(ctx context.Context, in map[string]any, value any) (map[string]any, error) {
	targets, ok := value.([]any)
	if !ok || len(targets) < 1 || len(targets) > 8 {
		return nil, core.NewError(core.InvalidArgument, "targets must be an array of 1 to 8 semantic targets")
	}
	for _, field := range []string{"symbol_id", "symbol_path", "path", "line", "column", "language"} {
		if _, exists := in[field]; exists {
			return nil, core.NewError(core.InvalidArgument, "targets cannot be combined with top-level target "+field)
		}
	}
	// Validate the whole batch before any LSP request. No partial result is
	// reported as success when one target is ambiguous, stale or malformed.
	seen := make(map[string]bool)
	for index, value := range targets {
		target, ok := value.(map[string]any)
		if !ok {
			return nil, core.NewError(core.InvalidArgument, fmt.Sprintf("targets[%d] must be an object", index))
		}
		for key := range target {
			switch key {
			case "symbol_id", "symbol_path", "path", "line", "column", "language":
			default:
				return nil, core.NewError(core.InvalidArgument, fmt.Sprintf("unsupported field in targets[%d]: %s", index, key))
			}
		}
		if err := targetOf(target).Validate(); err != nil {
			return nil, core.NewError(core.InvalidArgument, fmt.Sprintf("targets[%d]: %v", index, err))
		}
		key, _ := json.Marshal(target)
		if seen[string(key)] {
			return nil, core.NewError(core.InvalidArgument, "duplicate semantic target in batch")
		}
		seen[string(key)] = true
	}
	results := make([]map[string]any, 0, len(targets))
	// Reuse one current LSP documentSymbol tree per file in this batch.
	// The same snapshot is used for every target in that file.
	cache := make(map[string]inspectDocument)
	for index, value := range targets {
		target := value.(map[string]any)
		perItem := map[string]any{
			"view":      stringVal(in, "view"),
			"max_lines": intVal(in, "max_lines"),
		}
		for key, value := range target {
			perItem[key] = value
		}
		result, err := e.inspectSingleWithCache(ctx, perItem, cache)
		if err != nil {
			return nil, core.NewError(core.InvalidArgument, fmt.Sprintf("targets[%d]: %v", index, err))
		}
		results = append(results, result)
	}
	out := map[string]any{"results": results}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, core.WithCause(core.InternalError, "could not encode batched symbol result", err)
	}
	if len(data) > 32*1024 {
		return nil, core.NewError(core.InvalidArgument, "batched result exceeds 32 KiB; request fewer symbols or use view=signature")
	}
	return out, nil
}

// inspectSingle returns exactly the requested information for one LSP-verified
// declaration. It never reads outside the workspace, changes files, or silently
// claims that truncated source contains the whole declaration.
// inspectDocument is a per-MCP-call snapshot of an LSP document symbol tree.
// A batch uses one snapshot for several declarations of the same file.
type inspectDocument struct {
	doc   document.Document
	nodes []symbolNode
}

func (e *Engine) inspectSingle(ctx context.Context, in map[string]any) (map[string]any, error) {
	return e.inspectSingleWithCache(ctx, in, nil)
}

func (e *Engine) inspectSingleWithCache(ctx context.Context, in map[string]any, cache map[string]inspectDocument) (map[string]any, error) {
	view := stringVal(in, "view")
	if view == "" {
		view = "source"
	}
	if view != "source" && view != "signature" && view != "location" && view != "edit" {
		return nil, core.NewError(core.InvalidArgument, "view must be source, signature, location or edit")
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
	// When the agent already knows the file and declaration name, ask LSP
	// for that file's documentSymbol tree directly. e.target(symbol_path)
	// would first resolve this tree and then inspectSingle would fetch it
	// again; the direct path removes those redundant round trips.
	if stringVal(in, "symbol_id") == "" && stringVal(in, "path") != "" && stringVal(in, "symbol_path") != "" {
		return e.inspectPathSymbol(ctx, in, view, maxLines, cache)
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

	return e.renderInspected(d, selected, view, maxLines)
}

// inspectPathSymbol resolves a known declaration with one path-scoped
// documentSymbol query rather than a workspace-wide discovery. This retains
// LSP semantic symbol identity, nested path matching and ambiguity errors.
func (e *Engine) inspectPathSymbol(ctx context.Context, in map[string]any, view string, maxLines int, cache map[string]inspectDocument) (map[string]any, error) {
	p, err := e.profileForInput(in)
	if err != nil {
		return nil, err
	}
	path := stringVal(in, "path")
	key := p.SessionKey + "\x00" + p.LanguageID + "\x00" + path
	snapshot, found := cache[key]
	if !found {
		_, d, nodes, err := e.documentNodes(ctx, p, path)
		if err != nil {
			return nil, err
		}
		snapshot = inspectDocument{doc: d, nodes: nodes}
		if cache != nil {
			cache[key] = snapshot
		}
	}
	segments, anchored, err := parseSymbolPath(stringVal(in, "symbol_path"))
	if err != nil {
		return nil, err
	}
	matches := matchNodes(snapshot.nodes, segments, anchored)
	switch len(matches) {
	case 0:
		return nil, core.NewError(core.SymbolNotFound, "no symbol matches symbol_path "+stringVal(in, "symbol_path"))
	case 1:
		return e.renderInspected(snapshot.doc, matches[0], view, maxLines)
	default:
		// Never pick one of several declarations with the same name.
		list := make([]string, 0, 5)
		for i, node := range matches {
			if i == 5 {
				break
			}
			list = append(list, node.SymbolPath)
		}
		return nil, core.NewError(core.AmbiguousSymbol, fmt.Sprintf("%q matches %d symbols in %s: %s", stringVal(in, "symbol_path"), len(matches), path, strings.Join(list, ", ")))
	}
}

func (e *Engine) renderInspected(d document.Document, selected symbolNode, view string, maxLines int) (map[string]any, error) {
	path := relativeMust(e, d.Path)
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
	if view == "edit" {
		// Keep function body plus necessary syntax *outside* the LSP range:
		// Python route decorators, referenced module imports and assignments.
		// These are current source slices, not synthesised code or claims of
		// full dependency resolution.
		context := selectEditContext(d.Text, selected.Range, lines, maxSymbolSourceBytes)
		if context.Prefix != "" {
			lines = context.Prefix + lines
			result["code_start_line"] = selected.Range.Start.Line - strings.Count(context.Prefix, "\n")
		}
		if context.Imports != "" {
			result["imports"] = context.Imports
		}
		if context.Bindings != "" {
			result["module_bindings"] = context.Bindings
		}
		result["context_selection"] = "referenced-module-declarations"
		truncated = truncated || context.Truncated
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
	if r.Start.Line < 1 || r.End.Line < r.Start.Line {
		return "", false, core.NewError(core.InvalidArgument, "LSP symbol range outside source file")
	}
	// Validate the end line while locating the first requested line. Unlike
	// strings.SplitAfter on the entire source, this visits only the prefix
	// needed by the requested declaration and allocates no per-line strings.
	// An empty final line after a trailing newline is a valid source line.
	startOffset, offset := 0, 0
	for line := 1; line < r.End.Line; line++ {
		next := bytes.IndexByte(text[offset:], '\n')
		if next < 0 {
			return "", false, core.NewError(core.InvalidArgument, "LSP symbol range outside source file")
		}
		offset += next + 1
		if line+1 == r.Start.Line {
			startOffset = offset
		}
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
		// Inspect at most the bounded signature lines; do not split the
		// remainder of a potentially huge file just to locate the header.
		scan := startOffset
		for line := r.Start.Line; line <= availableEnd; line++ {
			next := len(text)
			if n := bytes.IndexByte(text[scan:], '\n'); n >= 0 {
				next = scan + n + 1
			}
			header := strings.TrimSpace(string(text[scan:next]))
			if strings.HasSuffix(header, "):") || strings.HasSuffix(header, ":") ||
				strings.HasSuffix(header, "{") || strings.Contains(header, "=> {") {
				availableEnd = line
				break
			}
			scan = next
		}
	}
	var sb strings.Builder
	lastLine := r.Start.Line - 1
	truncated := availableEnd < endLine
	scan := startOffset
	for line := r.Start.Line; line <= availableEnd; line++ {
		next := len(text)
		if n := bytes.IndexByte(text[scan:], '\n'); n >= 0 {
			next = scan + n + 1
		}
		sourceLine := text[scan:next]
		if sb.Len()+len(sourceLine) > maxBytes {
			remaining := maxBytes - sb.Len()
			if remaining > 0 {
				// Cut at a UTF-8 boundary; don't generate invalid source text.
				part := sourceLine[:remaining]
				for !utf8.Valid(part) {
					part = part[:len(part)-1]
				}
				sb.Write(part)
			}
			truncated = true
			lastLine = line
			break
		}
		sb.Write(sourceLine)
		lastLine = line
		scan = next
	}
	if lastLine < r.Start.Line {
		return "", false, core.NewError(core.InternalError, "empty declaration source range")
	}
	if signature && availableEnd < endLine {
		truncated = true
	}
	return sb.String(), truncated, nil
}
