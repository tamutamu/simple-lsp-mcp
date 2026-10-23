package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
)

// impact_analysis bounds. depth and limit are the only knobs a caller can
// turn; every other bound below is a hard ceiling that cannot be raised
// through tool input, so a highly-connected symbol degrades to a partial
// result instead of flooding the language server or hanging the request.
const (
	impactDefaultDepth  = 1
	impactMaxDepth      = 3
	impactMaxRequests   = 64
	impactMaxExpansions = 56
	impactMaxNodes      = 200
	impactMaxFanout     = 32
	impactMaxTotal      = time.Minute
)

// impactContext bounds the whole analysis, not just one LSP request:
// callContext only ever covers a single round trip, so without this a
// deeply-connected symbol could keep making individually-fast requests
// forever.
func (e *Engine) impactContext(ctx context.Context) (context.Context, context.CancelFunc) {
	total := e.Timeout * 4
	if total <= 0 || total > impactMaxTotal {
		total = impactMaxTotal
	}
	return context.WithTimeout(ctx, total)
}

// nodeKey identifies a call-hierarchy node independently of its symbol_id,
// which internal/symbol.Registry mints fresh (via crypto/rand) on every
// registration and so cannot be used to detect an already-visited node.
func nodeKey(path string, r core.Range) string {
	return fmt.Sprintf("%s#%d:%d-%d:%d", path, r.Start.Line, r.Start.Column, r.End.Line, r.End.Column)
}

// impactBudget accounts for every LSP round trip, expansion, and recorded
// node made by one impact_analysis call. It is created fresh per call and
// touched from a single goroutine only; the traversal in ImpactAnalysis
// never fans out across goroutines, so no locking is needed.
type impactBudget struct {
	ctx        context.Context
	requests   int
	expansions int
	nodes      int
	warnings   []string
	truncated  bool
}

func newImpactBudget(ctx context.Context) *impactBudget {
	return &impactBudget{ctx: ctx}
}

// request reserves one LSP round trip. It returns false once the deadline
// or the request budget has been reached.
func (b *impactBudget) request() bool {
	if b.ctx.Err() != nil {
		b.stop("analysis deadline exceeded")
		return false
	}
	if b.requests >= impactMaxRequests {
		b.stop(fmt.Sprintf("lsp request budget (%d) exhausted", impactMaxRequests))
		return false
	}
	b.requests++
	return true
}

// expand reserves one incomingCalls expansion, which also costs a request.
func (b *impactBudget) expand() bool {
	if b.expansions >= impactMaxExpansions {
		b.stop(fmt.Sprintf("expansion budget (%d) exhausted", impactMaxExpansions))
		return false
	}
	if !b.request() {
		return false
	}
	b.expansions++
	return true
}

// node reserves one recorded result node.
func (b *impactBudget) node() bool {
	if b.nodes >= impactMaxNodes {
		b.stop(fmt.Sprintf("node budget (%d) exhausted", impactMaxNodes))
		return false
	}
	b.nodes++
	return true
}

// stop records a reason the analysis became partial. Traversal keeps
// running for anything not gated by done(): a single node or one
// language-server error should not abort the whole call.
func (b *impactBudget) stop(reason string) {
	b.warnings = append(b.warnings, reason)
}

// truncate records a deliberately incomplete bounded search.
func (b *impactBudget) truncate(reason string) {
	b.truncated = true
	b.stop(reason)
}

// done reports whether the deadline or the request/expansion budget has
// been reached, so callers can stop opening new BFS levels.
func (b *impactBudget) done() bool {
	return b.ctx.Err() != nil || b.requests >= impactMaxRequests || b.expansions >= impactMaxExpansions
}

// impactMeta extends the shared result metadata with traversal accounting.
type impactMeta struct {
	core.Meta
	Depth           int   `json:"depth"`
	RequestedDepth  int   `json:"requested_depth"`
	Visited         int   `json:"visited"`
	LSPRequests     int   `json:"lsp_requests"`
	SkippedExternal int   `json:"skipped_external"`
	ElapsedMS       int64 `json:"elapsed_ms"`
}

// ImpactAnalysis estimates the blast radius of changing a symbol: its
// direct and transitive callers, references, implementations, and the
// files they touch. It is built entirely from existing LSP primitives
// (prepareCallHierarchy, callHierarchy/incomingCalls, references,
// implementation) under a hard traversal budget -- see impactBudget.
func (e *Engine) ImpactAnalysis(ctx context.Context, in map[string]any) (map[string]any, error) {
	start := time.Now()
	depth, err := core.ClampLimit(intVal(in, "depth"), impactMaxDepth, impactDefaultDepth)
	if err != nil {
		return nil, err
	}
	limit, err := e.limit(in)
	if err != nil {
		return nil, err
	}

	rootCtx, cancel := e.impactContext(ctx)
	defer cancel()
	cache := newFileCache()

	p, s, d, pos, err := e.target(rootCtx, in)
	if err != nil {
		return nil, err
	}
	rootNode, nonCallable := e.impactRootIdentity(rootCtx, p, s, d, pos, in)
	referenceOnly := nonCallable || !s.Capabilities().CallHierarchy
	if referenceOnly && !boolValDefault(in, "include_references", true) {
		return nil, core.NewError(core.InvalidArgument, "reference-only impact requires include_references=true")
	}
	if referenceOnly && !capable(s.Capabilities(), "references") {
		return nil, e.unsupported(p, "textDocument/references")
	}
	warmName := e.targetWarmName(in)
	if warmName == "" {
		warmName, _ = rootNode["name"].(string)
	}
	if err := e.warmProjectSymbolCandidates(rootCtx, p, s, warmName); err != nil {
		return nil, err
	}

	b := newImpactBudget(rootCtx)
	var items []protocol.CallHierarchyItem
	if !referenceOnly {
		if !b.request() {
			return nil, core.NewError(core.RequestTimeout, "impact_analysis deadline exceeded before it could start")
		}
		var raw json.RawMessage
		callCtx, cancelReq := e.callContext(rootCtx)
		err := s.Request(callCtx, "textDocument/prepareCallHierarchy", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}, "position": pos}, &raw)
		cancelReq()
		if err != nil {
			return nil, err // Do not hide unexpected LSP failures as a valid empty graph.
		}
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		if len(items) > 1 {
			return nil, core.NewError(core.InvalidArgument, "multiple call hierarchy items; specify an exact symbol")
		}
		if len(items) == 0 {
			referenceOnly = true
		}
	}
	if referenceOnly {
		if !boolValDefault(in, "include_references", true) {
			return nil, core.NewError(core.InvalidArgument, "reference-only impact requires include_references=true")
		}
		if !capable(s.Capabilities(), "references") {
			return nil, e.unsupported(p, "textDocument/references")
		}
		b.stop("root is not callable or has no call hierarchy; inspect direct references and enclosing declarations; transitive traversal may be incomplete")
	} else {
		rootNode = e.callItemIn(cache, p, s, items[0], nil)
		delete(rootNode, "from_ranges")
	}
	rootPath, _ := rootNode["path"].(string)
	rootSelRange, _ := rootNode["selection_range"].(core.Range)
	visited := map[string]bool{nodeKey(rootPath, rootSelRange): true}

	includeRefs := boolValDefault(in, "include_references", true)
	includeImpls := boolValDefault(in, "include_implementations", true)
	var refs, impls []core.Location
	refsOK, implsOK := false, false
	if includeRefs {
		if !capable(s.Capabilities(), "references") {
			b.stop(p.Name + " does not advertise textDocument/references")
		} else if b.request() {
			refs, err = e.locationsAt(rootCtx, cache, s, d.URI, pos, "textDocument/references", map[string]any{"context": map[string]bool{"includeDeclaration": false}})
			if err != nil {
				b.stop("references: " + err.Error())
			} else {
				refsOK = true
			}
		}
	}
	if includeImpls {
		if !capable(s.Capabilities(), "implementation") {
			b.stop(p.Name + " does not advertise textDocument/implementation")
		} else if b.request() {
			impls, err = e.locationsAt(rootCtx, cache, s, d.URI, pos, "textDocument/implementation", nil)
			if err != nil {
				b.stop("implementations: " + err.Error())
			} else {
				implsOK = true
			}
		}
	}

	// LSP references identify exact usages. Resolve each to its enclosing
	// documentSymbol before traversing further: a changed data declaration
	// can then lead to the functions and callers that actually depend on it.
	var dependentSymbols []any
	var owners []referenceOwner
	if referenceOnly && refsOK {
		dependentSymbols, owners = e.referenceOwners(rootCtx, p, refs, b)
	}
	ownerLimit := min(limit, impactMaxOwnerOutput)
	dependentTr := len(dependentSymbols) > ownerLimit
	if dependentTr {
		dependentSymbols = dependentSymbols[:ownerLimit]
		b.truncate(fmt.Sprintf("reference owner output capped at %d of %d symbols; narrow the query or inspect direct references", ownerLimit, len(owners)))
	}
	var direct, indirect []any
	skippedExternal := 0
	reachedDepth := 0

	frontier := items
	startLevel := 1
	if referenceOnly {
		startLevel = 2 // references are depth 1; callers of referencing functions are depth 2
		if depth >= 2 && refsOK {
			frontier = e.ownerCallFrontier(rootCtx, p, s, owners, b)
		}
	}
	for level := startLevel; level <= depth && len(frontier) > 0 && !b.done(); level++ {
		var next []protocol.CallHierarchyItem
		for _, item := range frontier {
			if !b.expand() {
				break
			}
			var resp json.RawMessage
			callCtx, cancelReq := e.callContext(rootCtx)
			err := s.Request(callCtx, "callHierarchy/incomingCalls", map[string]any{"item": item}, &resp)
			cancelReq()
			if err != nil {
				if rootCtx.Err() != nil {
					b.stop("analysis deadline exceeded")
					break
				}
				b.stop("incoming calls failed for " + item.Name + ": " + err.Error())
				continue
			}
			var xs []protocol.CallHierarchyIncomingCall
			if json.Unmarshal(resp, &xs) != nil {
				continue
			}
			for i, x := range xs {
				if i >= impactMaxFanout {
					b.truncate(fmt.Sprintf("fan-out capped at %d callers for %s", impactMaxFanout, item.Name))
					break
				}
				m := e.callItemIn(cache, p, s, x.From, x.FromRanges)
				if _, ok := m["symbol_id"]; !ok {
					skippedExternal++
					continue
				}
				mPath, _ := m["path"].(string)
				mSelRange, _ := m["selection_range"].(core.Range)
				k := nodeKey(mPath, mSelRange)
				if visited[k] {
					continue
				}
				visited[k] = true
				if !b.node() {
					break
				}
				m["depth"] = level
				if level > 1 {
					m["via"] = map[string]any{"name": item.Name}
				}
				if level == 1 {
					direct = append(direct, m)
				} else {
					indirect = append(indirect, m)
				}
				if level < depth {
					next = append(next, x.From)
				}
			}
		}
		frontier = next
		reachedDepth = level
	}

	directTr := len(direct) > limit
	if directTr {
		direct = direct[:limit]
	}
	indirectTr := len(indirect) > limit
	if indirectTr {
		indirect = indirect[:limit]
	}
	refsTr := len(refs) > limit
	if refsTr {
		refs = refs[:limit]
	}
	implsTr := len(impls) > limit
	if implsTr {
		impls = impls[:limit]
	}

	directOut := map[string]any{"callers": direct}
	if referenceOnly && refsOK {
		directOut["dependent_symbols"] = dependentSymbols
	}
	if refsOK {
		directOut["references"] = locationList(refs)
	}
	if implsOK {
		directOut["implementations"] = locationList(impls)
	}

	affected, affectedTr := affectedFiles(rootPath, direct, indirect, refs, impls, limit)

	mode := "call_hierarchy"
	if referenceOnly {
		mode = "references_only"
		if len(indirect) > 0 {
			mode = "reference_usage_graph"
		}
	}
	result := map[string]any{
		"analysis_mode":  mode,
		"root":           rootNode,
		"direct":         directOut,
		"indirect":       map[string]any{"callers": indirect},
		"affected_files": affected,
		"meta": impactMeta{
			Meta: core.Meta{
				Complete:  impactComplete(b, directTr, indirectTr, refsTr, implsTr, affectedTr, dependentTr),
				Truncated: impactTruncated(b, directTr, indirectTr, refsTr, implsTr, affectedTr, dependentTr),
				Warnings:  b.warnings,
			},
			Depth:           reachedDepth,
			RequestedDepth:  depth,
			Visited:         len(visited),
			LSPRequests:     b.requests,
			SkippedExternal: skippedExternal,
			ElapsedMS:       time.Since(start).Milliseconds(),
		},
	}
	return result, nil
}

// impactRootIdentity uses the symbol already returned by documentSymbol to
// decide whether a call hierarchy makes sense. For constants, fields, and
// types, references are still meaningful even though prepareCallHierarchy
// can error (gopls: "<name> is not a function"). No text matching is used.
func (e *Engine) impactRootIdentity(ctx context.Context, p language.Profile, s *session.Session, d document.Document, pos protocol.Position, in map[string]any) (map[string]any, bool) {
	path := relativeMust(e, d.Path)
	root := map[string]any{"language": p.Name, "path": path}
	var name, kind string
	if id := stringVal(in, "symbol_id"); id != "" {
		if rec, err := e.Symbols.Get(id); err == nil {
			name, kind = rec.Name, rec.Kind
			root["symbol_id"] = id
			root["range"] = rec.Range
			root["selection_range"] = rec.SelectionRange
			if rec.SymbolPath != "" {
				root["symbol_path"] = rec.SymbolPath
			}
		}
	} else {
		// position/symbol_path targets need the actual symbol kind, not a guess
		// from the identifier's spelling. DocumentSymbol is authoritative.
		position, err := document.FromLSP(d.Text, pos, s.Capabilities().PositionEncoding)
		if err == nil {
			_, _, nodes, err := e.documentNodes(ctx, p, path)
			if err == nil {
				for _, node := range nodes {
					if !impactPositionInRange(position, node.SelectionRange) {
						continue
					}
					name, kind = node.Name, node.Kind
					root["symbol_path"] = node.SymbolPath
					root["symbol_id"] = e.registerNode(p, path, d.URI, d.Hash, node)
					root["range"] = node.Range
					root["selection_range"] = node.SelectionRange
					break
				}
			}
		}
	}
	if name != "" {
		root["name"] = name
	}
	if kind != "" {
		root["kind"] = kind
	}
	// LSP call hierarchy describes callable functions/methods/constructors,
	// not declarations such as fields, constants, variables, or types.
	return root, kind != "" && kind != "function" && kind != "method" && kind != "constructor"
}

// impactPositionInRange treats an LSP selection range as half-open so
// selecting any character of a declaration name resolves the same symbol.
func impactPositionInRange(p core.Position, r core.Range) bool {
	if p.Line < r.Start.Line || p.Line > r.End.Line {
		return false
	}
	if p.Line == r.Start.Line && p.Column < r.Start.Column {
		return false
	}
	if p.Line == r.End.Line && p.Column >= r.End.Column {
		return false
	}
	return true
}

func locationList(locs []core.Location) []any {
	out := make([]any, len(locs))
	for i, l := range locs {
		out[i] = l
	}
	return out
}

// affectedFileReasonOrder fixes the order reasons appear in within one
// affected-file entry, independent of the order callers happened to be
// discovered in.
var affectedFileReasonOrder = []string{"definition", "caller", "indirect_caller", "reference", "implementation"}

type affectedFileEntry struct {
	path    string
	reasons map[string]bool
	count   int
}

// affectedFiles aggregates every reason one file is touched by a change,
// sorted by touch count descending and then by path, so the same input
// always produces the same output -- Go's map iteration order does not.
func affectedFiles(rootPath string, direct, indirect []any, refs, impls []core.Location, limit int) ([]any, bool) {
	var order []string
	byPath := map[string]*affectedFileEntry{}
	touch := func(path, reason string) {
		if path == "" {
			return
		}
		a, ok := byPath[path]
		if !ok {
			a = &affectedFileEntry{path: path, reasons: map[string]bool{}}
			byPath[path] = a
			order = append(order, path)
		}
		a.reasons[reason] = true
		a.count++
	}
	touch(rootPath, "definition")
	pathOf := func(v any) string {
		m, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		p, _ := m["path"].(string)
		return p
	}
	for _, m := range direct {
		touch(pathOf(m), "caller")
	}
	for _, m := range indirect {
		touch(pathOf(m), "indirect_caller")
	}
	for _, l := range refs {
		touch(l.Path, "reference")
	}
	for _, l := range impls {
		touch(l.Path, "implementation")
	}

	entries := make([]*affectedFileEntry, 0, len(order))
	for _, path := range order {
		entries = append(entries, byPath[path])
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].count != entries[j].count {
			return entries[i].count > entries[j].count
		}
		return entries[i].path < entries[j].path
	})
	tr := len(entries) > limit
	if tr {
		entries = entries[:limit]
	}
	out := make([]any, 0, len(entries))
	for _, a := range entries {
		var reasons []string
		for _, r := range affectedFileReasonOrder {
			if a.reasons[r] {
				reasons = append(reasons, r)
			}
		}
		out = append(out, map[string]any{"path": a.path, "reasons": reasons, "count": a.count})
	}
	return out, tr
}

// impactTruncated accounts for output limits as well as traversal limits.
func impactTruncated(b *impactBudget, outputs ...bool) bool {
	if b.truncated {
		return true
	}
	for _, truncated := range outputs {
		if truncated {
			return true
		}
	}
	return false
}

func impactComplete(b *impactBudget, outputs ...bool) bool {
	return len(b.warnings) == 0 && !impactTruncated(b, outputs...)
}
