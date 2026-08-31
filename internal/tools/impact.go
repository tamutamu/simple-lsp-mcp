package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
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
	if !s.Capabilities().CallHierarchy {
		return nil, e.unsupported(p, "textDocument/prepareCallHierarchy")
	}

	b := newImpactBudget(rootCtx)
	if !b.request() {
		return nil, core.NewError(core.RequestTimeout, "impact_analysis deadline exceeded before it could start")
	}
	var raw json.RawMessage
	{
		callCtx, cancelReq := e.callContext(rootCtx)
		err := s.Request(callCtx, "textDocument/prepareCallHierarchy", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}, "position": pos}, &raw)
		cancelReq()
		if err != nil {
			return nil, err
		}
	}
	var items []protocol.CallHierarchyItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	if len(items) != 1 {
		return nil, core.NewError(core.SymbolNotFound, "call hierarchy item was not found")
	}
	root := items[0]
	rootNode := e.callItemIn(cache, p, s, root, nil)
	delete(rootNode, "from_ranges")
	rootPath, _ := rootNode["path"].(string)
	rootSelRange, _ := rootNode["selection_range"].(core.Range)
	visited := map[string]bool{nodeKey(rootPath, rootSelRange): true}

	var direct, indirect []any
	skippedExternal := 0
	reachedDepth := 0

	frontier := []protocol.CallHierarchyItem{root}
	for level := 1; level <= depth && len(frontier) > 0 && !b.done(); level++ {
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
					b.stop(fmt.Sprintf("fan-out capped at %d callers for %s", impactMaxFanout, item.Name))
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
	if refsOK {
		directOut["references"] = locationList(refs)
	}
	if implsOK {
		directOut["implementations"] = locationList(impls)
	}

	affected, affectedTr := affectedFiles(rootPath, direct, indirect, refs, impls, limit)

	result := map[string]any{
		"root":           rootNode,
		"direct":         directOut,
		"indirect":       map[string]any{"callers": indirect},
		"affected_files": affected,
		"meta": impactMeta{
			Meta: core.Meta{
				Complete:  len(b.warnings) == 0,
				Truncated: directTr || indirectTr || refsTr || implsTr || affectedTr,
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
