package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
)

// maxProbeFiles bounds how many candidate files a workspace-wide
// symbol_path search will open when no path narrows the search to one file.
const maxProbeFiles = 8

// symbolLocation is one resolved symbol_path hit, holding everything needed
// to target it exactly like a symbol_id or a path+line+column would be.
type symbolLocation struct {
	Profile  language.Profile
	Session  *session.Session
	Document document.Document
	Node     symbolNode
}

// documentNodes fetches and walks the document symbol tree of one file. A
// flat SymbolInformation[] response (no parent/child structure) becomes a
// set of single-segment root-level nodes; matchNodes can still find them by
// name, just without any nesting.
func (e *Engine) documentNodes(ctx context.Context, p language.Profile, path string) (*session.Session, document.Document, []symbolNode, error) {
	_, s, d, err := e.document(ctx, path, p.Name)
	if err != nil {
		return nil, document.Document{}, nil, err
	}
	raw, err := e.requestDocumentSymbolTree(ctx, p, s, d)
	if err != nil {
		return nil, document.Document{}, nil, err
	}
	var tree []protocol.DocumentSymbol
	if json.Unmarshal(raw, &tree) == nil && tree != nil {
		return s, d, walkDocument(d.Text, s.Capabilities().PositionEncoding, tree, nil, ""), nil
	}
	var flat []protocol.SymbolInformation
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, document.Document{}, nil, err
	}
	nodes := make([]symbolNode, 0, len(flat))
	for _, v := range flat {
		rr, err := rangeFromText(d.Text, v.Location.Range, s.Capabilities().PositionEncoding)
		if err != nil {
			continue
		}
		segments := []string{v.Name}
		nodes = append(nodes, symbolNode{
			Name: v.Name, Kind: normalize.Kind(v.Kind),
			Segments: segments, SymbolPath: joinSymbolPath(segments),
			ContainerName: v.ContainerName, Range: rr, SelectionRange: rr,
		})
	}
	return s, d, nodes, nil
}

// candidateFiles asks the language server's workspace symbol index which
// files might contain a symbol path's leaf name. Results are not
// registered in the symbol registry: most of them will never be used, and
// registering unused candidates would only grow the registry for nothing.
func (e *Engine) candidateFiles(ctx context.Context, p language.Profile, leaf string) ([]string, error) {
	s, err := e.Sessions.For(p.SessionKey)
	if err != nil {
		return nil, err
	}
	var raw []protocol.WorkspaceSymbol
	callCtx, cancel := e.callContext(ctx)
	err = s.Request(callCtx, "workspace/symbol", map[string]string{"query": leaf}, &raw)
	cancel()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	for _, v := range raw {
		path, err := normalize.URIPath(e.WS, v.Location.URI)
		if err != nil || seen[path] {
			continue
		}
		seen[path] = true
		files = append(files, path)
	}
	return files, nil
}

// resolveSymbolPath finds every symbol matching symbolPath. When path is
// set, it searches only that file (one LSP request). Otherwise it asks
// workspace/symbol for candidate files by the path's leaf name and
// searches each of them, up to maxProbeFiles.
func (e *Engine) resolveSymbolPath(ctx context.Context, p language.Profile, symbolPath, path string) ([]symbolLocation, []string, error) {
	segments, anchored, err := parseSymbolPath(symbolPath)
	if err != nil {
		return nil, nil, err
	}
	if path != "" {
		s, d, nodes, err := e.documentNodes(ctx, p, path)
		if err != nil {
			return nil, nil, err
		}
		return locationsFor(p, s, d, matchNodes(nodes, segments, anchored)), nil, nil
	}
	leaf := segments[len(segments)-1]
	files, err := e.candidateFiles(ctx, p, leaf)
	if err != nil {
		return nil, nil, err
	}
	var warnings []string
	if len(files) > maxProbeFiles {
		warnings = append(warnings, fmt.Sprintf("more than %d files match %q; pass path to narrow the search", maxProbeFiles, leaf))
		files = files[:maxProbeFiles]
	}
	var hits []symbolLocation
	for _, f := range files {
		s, d, nodes, err := e.documentNodes(ctx, p, f)
		if err != nil {
			continue
		}
		hits = append(hits, locationsFor(p, s, d, matchNodes(nodes, segments, anchored))...)
	}
	return hits, warnings, nil
}

// resolveSymbolPathAcrossLanguages searches each configured LSP profile once
// when the caller provides only a symbol_path. Candidate files are then
// reclassified by extension so shared sessions (TypeScript/JavaScript) use
// the correct document language identifier.
func (e *Engine) resolveSymbolPathAcrossLanguages(ctx context.Context, symbolPath string) ([]symbolLocation, []string, error) {
	segments, anchored, err := parseSymbolPath(symbolPath)
	if err != nil {
		return nil, nil, err
	}
	leaf := segments[len(segments)-1]
	seenSession := map[string]bool{}
	seenLocation := map[string]bool{}
	var hits []symbolLocation
	var warnings []string
	configured := 0

	for _, base := range language.Profiles {
		if seenSession[base.SessionKey] || !e.Sessions.Configured(base.SessionKey) {
			continue
		}
		seenSession[base.SessionKey] = true
		configured++
		files, err := e.candidateFiles(ctx, base, leaf)
		if err != nil {
			warnings = append(warnings, base.SessionKey+": "+err.Error())
			continue
		}
		if len(files) > maxProbeFiles {
			warnings = append(warnings, fmt.Sprintf("%s: more than %d files match %q; specify language or path to narrow the search", base.SessionKey, maxProbeFiles, leaf))
			files = files[:maxProbeFiles]
		}
		for _, f := range files {
			p := base
			if inferred, inferErr := language.FromPath(f); inferErr == nil && inferred.SessionKey == base.SessionKey {
				p = inferred
			}
			s, d, nodes, err := e.documentNodes(ctx, p, f)
			if err != nil {
				continue
			}
			for _, hit := range locationsFor(p, s, d, matchNodes(nodes, segments, anchored)) {
				key := relativeMust(e, hit.Document.Path) + "#" + fmt.Sprint(hit.Node.SelectionRange)
				if seenLocation[key] {
					continue
				}
				seenLocation[key] = true
				hits = append(hits, hit)
			}
		}
	}
	if configured == 0 {
		return nil, warnings, core.NewError(core.InvalidArgument, "no language server profiles are configured")
	}
	return hits, warnings, nil
}

// resolveSymbolInput chooses the narrowest safe language strategy for a
// symbol_path request. Explicit language wins, path enables extension-based
// inference, and a bare symbol_path searches all configured profiles.
func (e *Engine) resolveSymbolInput(ctx context.Context, in map[string]any, symbolPath string) (language.Profile, []symbolLocation, []string, error) {
	if stringVal(in, "language") == "" && stringVal(in, "path") == "" {
		hits, warnings, err := e.resolveSymbolPathAcrossLanguages(ctx, symbolPath)
		if err != nil {
			return language.Profile{}, nil, warnings, err
		}
		if len(hits) == 1 {
			return hits[0].Profile, hits, warnings, nil
		}
		return language.Profile{}, hits, warnings, nil
	}
	p, err := e.profileForInput(in)
	if err != nil {
		return language.Profile{}, nil, nil, err
	}
	hits, warnings, err := e.resolveSymbolPath(ctx, p, symbolPath, stringVal(in, "path"))
	return p, hits, warnings, err
}

func locationsFor(p language.Profile, s *session.Session, d document.Document, nodes []symbolNode) []symbolLocation {
	out := make([]symbolLocation, len(nodes))
	for i, n := range nodes {
		out[i] = symbolLocation{Profile: p, Session: s, Document: d, Node: n}
	}
	return out
}

// ambiguousError renders an AMBIGUOUS_SYMBOL error listing up to five
// candidates inline, for tools whose output shape cannot carry a
// structured candidate list.
func (e *Engine) ambiguousError(symbolPath string, hits []symbolLocation) error {
	const maxInline = 5
	names := make([]string, 0, len(hits))
	for i, h := range hits {
		if i >= maxInline {
			names = append(names, fmt.Sprintf("and %d more", len(hits)-maxInline))
			break
		}
		names = append(names, fmt.Sprintf("%s (%s)", h.Node.SymbolPath, relativeMust(e, h.Document.Path)))
	}
	return core.NewError(core.AmbiguousSymbol, fmt.Sprintf("%q matches %d symbols: %s", symbolPath, len(hits), strings.Join(names, ", ")))
}

// defaultSourceLines caps how much source find_symbol includes by default.
const defaultSourceLines = 200

// FindSymbol resolves a symbol_path directly, without a prior search call.
// A single match returns normally; zero matches is a SYMBOL_NOT_FOUND
// error; more than one match returns a normal result carrying candidates
// instead of symbol, since an ambiguous outcome is not itself an error.
func (e *Engine) FindSymbol(ctx context.Context, in map[string]any) (map[string]any, error) {
	symbolPath := stringVal(in, "symbol_path")
	p, hits, warnings, err := e.resolveSymbolInput(ctx, in, symbolPath)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, core.NewError(core.SymbolNotFound, "no symbol matches symbol_path "+symbolPath)
	}
	if len(hits) > 1 {
		limit, err := e.limit(in)
		if err != nil {
			return nil, err
		}
		return e.ambiguousResult(symbolPath, hits, limit, warnings), nil
	}
	hit := hits[0]
	p = hit.Profile
	path := relativeMust(e, hit.Document.Path)
	id := e.registerNode(p, path, hit.Document.URI, hit.Document.Hash, hit.Node)
	sym := map[string]any{
		"symbol_id": id, "symbol_path": hit.Node.SymbolPath, "name": hit.Node.Name, "kind": hit.Node.Kind,
		"container_name": hit.Node.ContainerName, "detail": hit.Node.Detail, "language": p.Name, "path": path,
		"range": hit.Node.Range, "selection_range": hit.Node.SelectionRange,
	}
	meta := core.Meta{Complete: true, Warnings: warnings}
	if boolValDefault(in, "include_source", true) {
		source, truncated := sourceFor(hit.Document.Text, hit.Node.Range, intVal(in, "max_source_lines"))
		sym["source"] = source
		meta.SourceTruncated = truncated
	}
	return map[string]any{"symbol": sym, "meta": meta}, nil
}

// sourceFor extracts a symbol's source text, capping both the number of
// lines (maxLines, or defaultSourceLines when zero) and the total size.
func sourceFor(text []byte, r core.Range, maxLines int) (string, bool) {
	if maxLines <= 0 {
		maxLines = defaultSourceLines
	}
	capped := r
	truncated := false
	if capped.End.Line-capped.Start.Line+1 > maxLines {
		capped.End.Line = capped.Start.Line + maxLines - 1
		truncated = true
	}
	source := sliceRange(text, capped)
	if len(source) > hoverMaxBytes {
		source = source[:hoverMaxBytes]
		truncated = true
	}
	return string(source), truncated
}

// ambiguousResult renders the candidate payload shared by the high-level
// tools when a symbol_path matches more than one symbol. Each candidate
// carries its own symbol_id, so the next call can target it directly
// instead of repeating the ambiguous search.
func (e *Engine) ambiguousResult(symbolPath string, hits []symbolLocation, limit int, warnings []string) map[string]any {
	tr := len(hits) > limit
	if tr {
		hits = hits[:limit]
	}
	candidates := make([]map[string]any, len(hits))
	for i, h := range hits {
		path := relativeMust(e, h.Document.Path)
		p := h.Profile
		id := e.registerNode(p, path, h.Document.URI, h.Document.Hash, h.Node)
		candidates[i] = map[string]any{
			"symbol_id": id, "symbol_path": h.Node.SymbolPath, "name": h.Node.Name, "kind": h.Node.Kind,
			"language": p.Name, "path": path, "range": h.Node.Range, "selection_range": h.Node.SelectionRange,
		}
	}
	warnings = append(warnings, fmt.Sprintf("%q matches %d symbols; pass a more specific symbol_path, a path, language, or the symbol_id of one candidate", symbolPath, len(hits)))
	return map[string]any{"symbol": nil, "ambiguous": true, "candidates": candidates, "meta": core.Meta{Complete: true, Truncated: tr, Warnings: warnings}}
}

// maxOutlineDepth bounds how many levels of children get_symbol_outline
// will recurse into.
const maxOutlineDepth = 3

// SymbolOutline lists the direct children of a symbol_path, or the
// top-level symbols of a file when symbol_path is omitted. It never
// returns source text: get_symbol or get_symbol_context can, once the
// caller has decided which child is worth reading.
func (e *Engine) SymbolOutline(ctx context.Context, in map[string]any) (map[string]any, error) {
	depth, err := core.ClampLimit(intVal(in, "depth"), maxOutlineDepth, 1)
	if err != nil {
		return nil, err
	}
	limit, err := e.limit(in)
	if err != nil {
		return nil, err
	}
	symbolPath := stringVal(in, "symbol_path")
	path := stringVal(in, "path")

	if symbolPath == "" {
		if path == "" {
			return nil, core.NewError(core.InvalidArgument, "specify symbol_path, path, or both")
		}
		p, err := e.profileForInput(in)
		if err != nil {
			return nil, err
		}
		_, d, nodes, err := e.documentNodes(ctx, p, path)
		if err != nil {
			return nil, err
		}
		docPath := relativeMust(e, d.Path)
		children, tr := e.outlineChildren(p, docPath, d.URI, d.Hash, nodes, depth, limit)
		return map[string]any{"children": children, "meta": core.Meta{Complete: true, Truncated: tr}}, nil
	}

	p, hits, warnings, err := e.resolveSymbolInput(ctx, in, symbolPath)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, core.NewError(core.SymbolNotFound, "no symbol matches symbol_path "+symbolPath)
	}
	if len(hits) > 1 {
		return e.ambiguousResult(symbolPath, hits, limit, warnings), nil
	}
	hit := hits[0]
	p = hit.Profile
	docPath := relativeMust(e, hit.Document.Path)
	parentID := e.registerNode(p, docPath, hit.Document.URI, hit.Document.Hash, hit.Node)
	parent := map[string]any{
		"symbol_id": parentID, "symbol_path": hit.Node.SymbolPath, "name": hit.Node.Name,
		"kind": hit.Node.Kind, "detail": hit.Node.Detail, "language": p.Name, "path": docPath,
		"range": hit.Node.Range, "selection_range": hit.Node.SelectionRange,
	}
	children, tr := e.outlineChildren(p, docPath, hit.Document.URI, hit.Document.Hash, hit.Node.Children, depth, limit)
	return map[string]any{"parent": parent, "children": children, "meta": core.Meta{Complete: true, Truncated: tr, Warnings: warnings}}, nil
}

// outlineChildren renders and registers up to limit direct children,
// recursing depth-1 further levels into each. It never includes source
// text and registers only the nodes it actually returns.
func (e *Engine) outlineChildren(p language.Profile, path, uri, fileHash string, nodes []symbolNode, depth, limit int) ([]any, bool) {
	tr := len(nodes) > limit
	if tr {
		nodes = nodes[:limit]
	}
	out := make([]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, e.outlineNode(p, path, uri, fileHash, n, depth))
	}
	return out, tr
}

func (e *Engine) outlineNode(p language.Profile, path, uri, fileHash string, n symbolNode, depth int) map[string]any {
	id := e.registerNode(p, path, uri, fileHash, n)
	x := map[string]any{
		"symbol_id": id, "symbol_path": n.SymbolPath, "name": n.Name, "kind": n.Kind,
		"detail": n.Detail, "path": path, "range": n.Range, "selection_range": n.SelectionRange,
		"child_count": len(n.Children),
	}
	if depth > 1 && len(n.Children) > 0 {
		children := make([]any, 0, len(n.Children))
		for _, c := range n.Children {
			children = append(children, e.outlineNode(p, path, uri, fileHash, c, depth-1))
		}
		x["children"] = children
	}
	return x
}

// defaultSectionLimit caps each section of get_symbol_context independently
// of --max-results: a references list capped at 500 would dwarf the rest
// of a single-call answer.
const defaultSectionLimit = 20

// sectionLimit resolves the per-section cap for an aggregate request.
func (e *Engine) sectionLimit(in map[string]any) (int, error) {
	return core.ClampLimit(intVal(in, "limit"), e.MaxResults, defaultSectionLimit)
}

// includeSection reports whether one get_symbol_context section was
// requested. All sections are included when "include" is omitted or empty.
func includeSection(in map[string]any, name string) bool {
	values, ok := in["include"].([]any)
	if !ok || len(values) == 0 {
		return true
	}
	for _, v := range values {
		if s, _ := v.(string); s == name {
			return true
		}
	}
	return false
}

// SymbolContext gathers everything about one symbol in a single call: its
// source, callers, callees, references, and implementations. Each section
// is best-effort -- a missing capability or a failed sub-request drops
// that section and records why in meta.warnings, rather than failing the
// whole call. Only a failure to resolve the symbol itself is a hard error.
func (e *Engine) SymbolContext(ctx context.Context, in map[string]any) (map[string]any, error) {
	var (
		p                                            language.Profile
		d                                            document.Document
		name, kind, containerName, detail, symbolPth string
		symRange, symSelRange                        core.Range
		hasRange                                     bool
		warnings                                     []string
		targetSymbolID                               string
	)

	switch {
	case stringVal(in, "symbol_path") != "":
		symbolPath := stringVal(in, "symbol_path")
		var hits []symbolLocation
		var w []string
		var err error
		p, hits, w, err = e.resolveSymbolInput(ctx, in, symbolPath)
		if err != nil {
			return nil, err
		}
		if len(hits) == 0 {
			return nil, core.NewError(core.SymbolNotFound, "no symbol matches symbol_path "+symbolPath)
		}
		if len(hits) > 1 {
			limit, err := e.sectionLimit(in)
			if err != nil {
				return nil, err
			}
			return e.ambiguousResult(symbolPath, hits, limit, w), nil
		}
		hit := hits[0]
		p = hit.Profile
		d = hit.Document
		warnings = w
		docPath := relativeMust(e, d.Path)
		targetSymbolID = e.registerNode(p, docPath, d.URI, d.Hash, hit.Node)
		name, kind, containerName, detail, symbolPth = hit.Node.Name, hit.Node.Kind, hit.Node.ContainerName, hit.Node.Detail, hit.Node.SymbolPath
		symRange, symSelRange, hasRange = hit.Node.Range, hit.Node.SelectionRange, true

	case stringVal(in, "symbol_id") != "":
		id := stringVal(in, "symbol_id")
		rec, err := e.Symbols.Get(id)
		if err != nil {
			return nil, err
		}
		pp, _, dd, _, err := e.target(ctx, in)
		if err != nil {
			return nil, err
		}
		p, d = pp, dd
		targetSymbolID = id
		name, kind, containerName, detail, symbolPth = rec.Name, rec.Kind, rec.ContainerName, rec.Detail, rec.SymbolPath
		symRange, symSelRange, hasRange = rec.Range, rec.SelectionRange, true

	default:
		pp, _, dd, _, err := e.target(ctx, in)
		if err != nil {
			return nil, err
		}
		p, d = pp, dd
	}

	subIn := map[string]any{"language": p.Name}
	if targetSymbolID != "" {
		subIn["symbol_id"] = targetSymbolID
	} else {
		subIn["path"] = stringVal(in, "path")
		subIn["line"] = intVal(in, "line")
		subIn["column"] = intVal(in, "column")
	}

	path := relativeMust(e, d.Path)
	symbol := map[string]any{"language": p.Name, "path": path}
	for k, v := range map[string]string{"symbol_path": symbolPth, "name": name, "kind": kind, "container_name": containerName, "detail": detail} {
		if v != "" {
			symbol[k] = v
		}
	}
	if targetSymbolID != "" {
		symbol["symbol_id"] = targetSymbolID
	}
	if hasRange {
		symbol["range"] = symRange
		symbol["selection_range"] = symSelRange
	}

	limit, err := e.sectionLimit(in)
	if err != nil {
		return nil, err
	}
	rootCtx, cancel := context.WithTimeout(ctx, 3*e.Timeout)
	defer cancel()

	out := map[string]any{"symbol": symbol}
	complete := true
	if includeSection(in, "source") {
		if hasRange {
			text, truncated := sourceFor(d.Text, symRange, intVal(in, "max_source_lines"))
			out["source"] = map[string]any{"text": text, "truncated": truncated}
		} else {
			warnings = append(warnings, "source: no declared symbol at this position")
		}
	}

	type sectionResult struct {
		name    string
		value   any
		ok      bool
		warning string
	}
	results := make(chan sectionResult, 4)
	active := 0
	launch := func(name string, fn func() (any, bool, string)) {
		active++
		go func() {
			v, ok, warning := fn()
			results <- sectionResult{name: name, value: v, ok: ok, warning: warning}
		}()
	}
	if includeSection(in, "incoming_calls") {
		launch("incoming_calls", func() (any, bool, string) { return e.contextCalls(rootCtx, "get_incoming_calls", subIn, limit) })
	}
	if includeSection(in, "outgoing_calls") {
		launch("outgoing_calls", func() (any, bool, string) { return e.contextCalls(rootCtx, "get_outgoing_calls", subIn, limit) })
	}
	if includeSection(in, "references") {
		launch("references", func() (any, bool, string) { return e.contextReferences(rootCtx, subIn, limit) })
	}
	if includeSection(in, "implementations") {
		launch("implementations", func() (any, bool, string) { return e.contextImplementations(rootCtx, subIn, limit) })
	}
	for i := 0; i < active; i++ {
		r := <-results
		if r.ok {
			out[r.name] = r.value
		} else {
			warnings = append(warnings, r.warning)
			complete = false
		}
	}

	out["meta"] = core.Meta{Complete: complete, Warnings: warnings}
	return out, nil
}

// contextCalls runs get_incoming_calls or get_outgoing_calls as a sub-call
// and projects each result down to the fields useful in a summary.
func (e *Engine) contextCalls(ctx context.Context, name string, subIn map[string]any, limit int) ([]any, bool, string) {
	res, err := e.Hierarchy(ctx, name, withLimit(subIn, limit))
	if err != nil {
		return nil, false, name + ": " + err.Error()
	}
	calls, _ := res["calls"].([]any)
	out := make([]any, 0, len(calls))
	for _, c := range calls {
		out = append(out, compactCall(c))
	}
	return out, true, ""
}

// compactCall projects a call-hierarchy item down to symbol_id,
// symbol_path, name, kind, path, and the starting line, dropping
// from_ranges and the full selection_range.
func compactCall(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := map[string]any{}
	for _, k := range []string{"symbol_id", "symbol_path", "name", "kind", "path"} {
		if x, ok := m[k]; ok {
			out[k] = x
		}
	}
	if r, ok := m["range"].(core.Range); ok {
		out["line"] = r.Start.Line
	}
	return out
}

// contextReferences runs find_references as a sub-call and collapses the
// flat location list into one entry per file with just the starting
// lines, which is enough to decide where to look next without paying for
// a full location per hit.
func (e *Engine) contextReferences(ctx context.Context, subIn map[string]any, limit int) ([]any, bool, string) {
	res, err := e.Relationship(ctx, "find_references", "textDocument/references", "references", withLimit(subIn, limit))
	if err != nil {
		return nil, false, "references: " + err.Error()
	}
	locs, _ := res["locations"].([]core.Location)
	return groupReferences(locs), true, ""
}

// groupReferences collapses a flat location list into one entry per file.
func groupReferences(locs []core.Location) []any {
	type fileRefs struct {
		path  string
		lines []int
	}
	var order []string
	byPath := map[string]*fileRefs{}
	for _, l := range locs {
		a, ok := byPath[l.Path]
		if !ok {
			a = &fileRefs{path: l.Path}
			byPath[l.Path] = a
			order = append(order, l.Path)
		}
		a.lines = append(a.lines, l.Range.Start.Line)
	}
	out := make([]any, 0, len(order))
	for _, path := range order {
		a := byPath[path]
		out = append(out, map[string]any{"path": a.path, "count": len(a.lines), "lines": a.lines})
	}
	return out
}

// contextImplementations runs find_implementations as a sub-call.
func (e *Engine) contextImplementations(ctx context.Context, subIn map[string]any, limit int) ([]core.Location, bool, string) {
	res, err := e.Relationship(ctx, "find_implementations", "textDocument/implementation", "implementation", withLimit(subIn, limit))
	if err != nil {
		return nil, false, "implementations: " + err.Error()
	}
	locs, _ := res["locations"].([]core.Location)
	return locs, true, ""
}

// withLimit copies a sub-call's target arguments and applies one section's limit.
func withLimit(subIn map[string]any, limit int) map[string]any {
	out := make(map[string]any, len(subIn)+1)
	for k, v := range subIn {
		out[k] = v
	}
	out["limit"] = limit
	return out
}
