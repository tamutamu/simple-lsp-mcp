package tools

import (
	"context"
	"fmt"
	"sort"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
)

// Inspect at most this many distinct source files when mapping LSP references
// back to enclosing declarations; incomplete mapping is always disclosed.
const impactMaxOwnerFiles = 16
const impactMaxOwnerReferences = 128
const impactMaxOwnerExpansion = 12
const impactMaxOwnerOutput = 24

type referenceOwner struct {
	item    map[string]any
	node    symbolNode
	session *session.Session
	doc     document.Document
}

// innermostEnclosingSymbol prefers the deepest LSP documentSymbol covering
// the *reference location*. This differs fundamentally from substring-based
// matching: two homonymous identifiers resolve to independent locations.
func innermostEnclosingSymbol(nodes []symbolNode, p core.Position) (symbolNode, bool) {
	for _, node := range nodes {
		if !impactPositionInRange(p, node.Range) {
			continue
		}
		if nested, ok := innermostEnclosingSymbol(node.Children, p); ok {
			return nested, true
		}
		return node, true
	}
	return symbolNode{}, false
}

// referenceOwners combines textDocument/references and documentSymbol to show
// which declarations actually *use* a variable, type or field. LSP reference
// locations remain the sole source of truth; this never guesses by name.
func (e *Engine) referenceOwners(ctx context.Context, p language.Profile, refs []core.Location, b *impactBudget) ([]any, []referenceOwner) {
	grouped := map[string][]core.Location{}
	// LSPs need not order references consistently. Stabilize bounded selection
	// without changing the order of the raw reference locations in the result.
	ordered := append([]core.Location(nil), refs...)
	sort.Slice(ordered, func(i, j int) bool {
		a, z := ordered[i], ordered[j]
		if a.Path != z.Path {
			return a.Path < z.Path
		}
		if a.Range.Start.Line != z.Range.Start.Line {
			return a.Range.Start.Line < z.Range.Start.Line
		}
		return a.Range.Start.Column < z.Range.Start.Column
	})
	limit := len(ordered)
	if limit > impactMaxOwnerReferences {
		b.truncate(fmt.Sprintf("reference ownership inspected first %d of %d LSP locations", impactMaxOwnerReferences, len(refs)))
		limit = impactMaxOwnerReferences
	}
	for _, ref := range ordered[:limit] {
		grouped[ref.Path] = append(grouped[ref.Path], ref)
	}
	paths := make([]string, 0, len(grouped))
	for path := range grouped {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) > impactMaxOwnerFiles {
		b.truncate(fmt.Sprintf("reference ownership capped at %d of %d source files", impactMaxOwnerFiles, len(paths)))
		paths = paths[:impactMaxOwnerFiles]
	}
	owners := []referenceOwner{}
	for _, path := range paths {
		if !b.request() {
			break
		}
		pp := p
		if inferred, err := language.FromPath(path); err == nil && inferred.SessionKey == p.SessionKey {
			pp = inferred
		}
		s, d, nodes, err := e.documentNodes(ctx, pp, path)
		if err != nil {
			b.stop("cannot resolve reference owners in " + path + ": " + err.Error())
			continue
		}
		byKey := map[string]int{}
		for _, ref := range grouped[path] {
			node, ok := innermostEnclosingSymbol(nodes, ref.Range.Start)
			if !ok {
				continue
			} // imports and module-level expressions have no enclosing declaration
			key := nodeKey(path, node.SelectionRange)
			idx, exists := byKey[key]
			if !exists {
				entry := map[string]any{
					"name": node.Name, "kind": node.Kind, "symbol_path": node.SymbolPath,
					"path": path, "range": node.Range,
					"symbol_id":       e.registerNode(pp, path, d.URI, d.Hash, node),
					"reference_count": 0, "reference_lines": []int{},
				}
				if semanticTestFile(path) {
					entry["test_candidate"] = true
				}
				owners = append(owners, referenceOwner{item: entry, node: node, session: s, doc: d})
				idx = len(owners) - 1
				byKey[key] = idx
			}
			item := owners[idx].item
			item["reference_count"] = item["reference_count"].(int) + 1
			item["reference_lines"] = append(item["reference_lines"].([]int), ref.Range.Start.Line)
		}
	}
	// Return the most-used declarations first. For crowded repositories the
	// first N caller-expansion roots must not depend on filesystem walk order.
	sort.SliceStable(owners, func(i, j int) bool {
		a, z := owners[i].item, owners[j].item
		if a["reference_count"].(int) != z["reference_count"].(int) {
			return a["reference_count"].(int) > z["reference_count"].(int)
		}
		if a["path"] != z["path"] {
			return a["path"].(string) < z["path"].(string)
		}
		return a["symbol_path"].(string) < z["symbol_path"].(string)
	})
	out := make([]any, len(owners))
	for i, owner := range owners {
		out[i] = owner.item
	}
	return out, owners
}

// impactCallableOwnerKind includes TypeScript arrow-function declarations,
// which most LSPs report as variables/constants instead of functions.
func impactCallableOwnerKind(kind string) bool {
	switch kind {
	case "function", "method", "constructor", "variable", "constant":
		return true
	default:
		return false
	}
}

// ownerCallFrontier makes the already-resolved enclosing functions into
// call-hierarchy starting points. This enables transitive queries such as
// changed constant -> referencing function -> its callers. Failures do not
// fabricate a call graph; they leave explicit warnings and partial results.
func (e *Engine) ownerCallFrontier(ctx context.Context, p language.Profile, rootSession *session.Session, owners []referenceOwner, b *impactBudget) []protocol.CallHierarchyItem {
	var frontier []protocol.CallHierarchyItem
	// Exclude non-callable declarations *before* applying the hard expansion
	// limit; otherwise a dozen classes/fields can starve actual functions.
	candidates := make([]referenceOwner, 0, len(owners))
	for _, owner := range owners {
		if impactCallableOwnerKind(owner.node.Kind) {
			candidates = append(candidates, owner)
		}
	}
	if len(candidates) > impactMaxOwnerExpansion {
		b.truncate(fmt.Sprintf("call expansion capped at %d of %d callable reference owners", impactMaxOwnerExpansion, len(candidates)))
		candidates = candidates[:impactMaxOwnerExpansion]
	}
	for _, owner := range candidates {
		if owner.session != rootSession {
			b.stop("referencing symbol belongs to a different LSP session: " + owner.node.SymbolPath)
			continue
		}
		if !owner.session.Capabilities().CallHierarchy {
			b.stop("call hierarchy not supported for " + owner.node.SymbolPath)
			continue
		}
		if !b.request() {
			break
		}
		if projectWarmSupported(p) {
			if err := e.warmProjectSymbolCandidates(ctx, p, owner.session, owner.node.Name); err != nil {
				b.stop("could not warm references for " + owner.node.SymbolPath + ": " + err.Error())
				continue
			}
		}
		pos, err := document.ToLSP(owner.doc.Text, owner.node.SelectionRange.Start, owner.session.Capabilities().PositionEncoding)
		if err != nil {
			b.stop("invalid LSP position for " + owner.node.SymbolPath)
			continue
		}
		callCtx, cancel := e.callContext(ctx)
		var xs []protocol.CallHierarchyItem
		err = owner.session.Request(callCtx, "textDocument/prepareCallHierarchy", map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: owner.doc.URI}, "position": pos}, &xs)
		cancel()
		if err != nil {
			b.stop("prepare call hierarchy for " + owner.node.SymbolPath + ": " + err.Error())
			continue
		}
		if len(xs) != 1 {
			if len(xs) > 1 {
				b.stop("ambiguous call hierarchy for " + owner.node.SymbolPath)
			}
			continue
		}
		frontier = append(frontier, xs[0])
	}
	return frontier
}
