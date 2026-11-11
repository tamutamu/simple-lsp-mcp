package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
)

const (
	defaultInspectRelationLimit = 8
	maxInspectRelationLimit     = 20
	maxInspectRelationSections  = 6
	maxInspectRelatedBytes      = 48 * 1024
)

// inspectRelationMethods maps the requested fact to the *actual* LSP operation.
// Unlike a name-based search, every query starts at the selected declaration's
// exact LSP selection position.
var inspectRelationMethods = map[string]string{
	"references":      "textDocument/references",
	"implementations": "textDocument/implementation",
	"type_definition": "textDocument/typeDefinition",
	"declaration":     "textDocument/declaration",
	"incoming_calls":  "get_incoming_calls",
	"outgoing_calls":  "get_outgoing_calls",
	"supertypes":      "get_supertypes",
	"subtypes":        "get_subtypes",
}

// parseInspectRelations validates *before* invoking any LSP operation, so an
// invalid request does not trigger costly project warming or partial work.
func parseInspectRelations(in map[string]any) ([]string, int, error) {
	limit := defaultInspectRelationLimit
	if _, specified := in["relation_limit"]; specified {
		limit = intVal(in, "relation_limit")
	}
	if limit < 1 || limit > maxInspectRelationLimit {
		return nil, 0, core.NewError(core.InvalidArgument, "relation_limit must be between 1 and 20 (bounded context only); for exhaustive references use find_references with limit instead")
	}
	raw, present := in["related"]
	if !present {
		return nil, limit, nil
	}
	var entries []any
	switch v := raw.(type) {
	case []any:
		entries = v
	case []string:
		for _, name := range v {
			entries = append(entries, name)
		}
	default:
		return nil, 0, core.NewError(core.InvalidArgument, "related must be an array of relation names")
	}
	if len(entries) < 1 || len(entries) > maxInspectRelationSections {
		return nil, 0, core.NewError(core.InvalidArgument, "related must request between 1 and 6 relations")
	}
	if _, batch := in["targets"]; batch {
		return nil, 0, core.NewError(core.InvalidArgument, "related cannot be combined with targets; inspect one declaration at a time")
	}
	seen := make(map[string]bool, len(entries))
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		name, ok := entry.(string)
		if !ok || inspectRelationMethods[name] == "" {
			return nil, 0, core.NewError(core.InvalidArgument, fmt.Sprintf("unknown related LSP relation %q", entry))
		}
		if seen[name] {
			return nil, 0, core.NewError(core.InvalidArgument, "related contains duplicate relation "+name)
		}
		seen[name] = true
		result = append(result, name)
	}
	return result, limit, nil
}

// inspectWithRelations adds selected, bounded LSP facts to one exact source
// declaration. An unavailable capability is NEVER silently presented as an
// empty relation: failures and truncation are explicit in related_meta.
func (e *Engine) inspectWithRelations(ctx context.Context, out map[string]any, names []string, limit int) (map[string]any, error) {
	selection, ok := out["selection_range"].(core.Range)
	if !ok || selection.Start.Line < 1 || selection.Start.Column < 1 {
		return nil, core.NewError(core.InternalError, "selected declaration has no valid LSP selection range")
	}
	subIn := map[string]any{
		"path": out["path"], "line": selection.Start.Line,
		"column": selection.Start.Column, "limit": limit,
	}
	sections := make(map[string]any, len(names))
	failed := make(map[string]string)
	truncated := make([]string, 0)
	// Pyright/TypeScript may scan and open candidate files before each
	// cross-file relation. When two or more requested sections need the same
	// project preparation, do it just once for this request. Single relations
	// keep their original direct LSP path without extra setup.
	if inspectWarmRelationCount(names) >= 2 {
		path := out["path"].(string)
		if profile, err := language.FromPath(path); err == nil && projectWarmSupported(profile) {
			p, server, doc, err := e.document(ctx, path, "")
			if err == nil {
				var posErr error
				position, conversionErr := document.ToLSP(doc.Text, selection.Start, server.Capabilities().PositionEncoding)
				if conversionErr != nil {
					posErr = conversionErr
				} else {
					name := e.warmNameForPosition(ctx, p, server, doc, position, subIn)
					posErr = e.warmProjectSymbolCandidates(ctx, p, server, name)
					if posErr == nil && inspectNeedsPrime(names) {
						posErr = e.primeProjectSemantics(ctx, p, server, doc, position)
					}
				}
				err = posErr
			}
			if err != nil {
				for _, name := range names {
					if inspectNeedsWarm(name) {
						failed[name] = "project LSP preparation failed: " + err.Error()
					}
				}
			} else {
				ctx = markProjectWarmed(ctx)
			}
		}
	}
	for _, name := range names {
		if _, alreadyFailed := failed[name]; alreadyFailed {
			continue
		}
		if err := ctx.Err(); err != nil {
			failed[name] = err.Error()
			continue
		}
		var res map[string]any
		var err error
		switch name {
		case "incoming_calls", "outgoing_calls", "supertypes", "subtypes":
			res, err = e.Hierarchy(ctx, inspectInternalToolName(name), subIn)
		default:
			res, err = e.Relationship(ctx, inspectInternalToolName(name), inspectRelationMethods[name], relationCapability(name), subIn)
		}
		if err != nil {
			failed[name] = err.Error()
			continue
		}
		switch name {
		case "incoming_calls", "outgoing_calls":
			sections[name] = compactRelatedItems(res["calls"])
		case "supertypes", "subtypes":
			sections[name] = compactRelatedItems(res["types"])
		case "references":
			locs, _ := res["locations"].([]core.Location)
			sections[name] = groupRelatedReferences(locs)
		default:
			locs, _ := res["locations"].([]core.Location)
			sections[name] = compactRelatedLocations(locs)
		}
		if meta, ok := res["meta"].(core.Meta); ok && !meta.Complete {
			truncated = append(truncated, name)
		}
	}
	out["related"] = sections
	// A "complete" result means every requested relation was retrieved
	// without server failure or per-section truncation; not that static
	// analysis predicts every possible runtime behavior.
	complete := len(failed) == 0 && len(truncated) == 0
	out["related_meta"] = map[string]any{
		"complete": complete, "failed": failed, "truncated": truncated,
		"limit_per_relation": limit,
	}
	if meta, ok := out["meta"].(core.Meta); ok {
		meta.Complete = meta.Complete && complete
		meta.Truncated = meta.Truncated || len(truncated) > 0
		out["meta"] = meta
	} else {
		out["meta"] = core.Meta{Complete: complete, Truncated: len(truncated) > 0}
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, core.WithCause(core.InternalError, "could not marshal semantic context", err)
	}
	if len(encoded) > maxInspectRelatedBytes {
		return nil, core.NewError(core.IncompleteSearch, "semantic context exceeds 48 KiB; request fewer relations or fewer lines")
	}
	return out, nil
}

// Only these operations need project-wide candidate warming in the
// individual LSP tools. Type/definition queries do not.
func inspectNeedsWarm(name string) bool {
	switch name {
	case "references", "implementations", "incoming_calls", "subtypes":
		return true
	}
	return false
}

func inspectWarmRelationCount(names []string) int {
	count := 0
	for _, name := range names {
		if inspectNeedsWarm(name) {
			count++
		}
	}
	return count
}

func inspectNeedsPrime(names []string) bool {
	for _, name := range names {
		if name == "references" || name == "implementations" {
			return true
		}
	}
	return false
}

// Relationship and Hierarchy use the registered MCP tool names to select
// required LSP parameters and the call-vs-type hierarchy. The public
// related[] values are intentionally shorter. Never pass them through as-is.
func inspectInternalToolName(name string) string {
	switch name {
	case "references":
		return "find_references"
	case "implementations":
		return "find_implementations"
	default:
		return "get_" + name
	}
}

func relationCapability(name string) string {
	switch name {
	case "references":
		return "references"
	case "implementations":
		return "implementation"
	case "type_definition":
		return "typeDefinition"
	case "declaration":
		return "declaration"
	default:
		return ""
	}
}

func compactRelatedLocations(locs []core.Location) []map[string]any {
	out := make([]map[string]any, 0, len(locs))
	for _, loc := range locs {
		out = append(out, map[string]any{"path": loc.Path, "line": loc.Range.Start.Line, "column": loc.Range.Start.Column})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a["path"] != b["path"] {
			return a["path"].(string) < b["path"].(string)
		}
		if a["line"] != b["line"] {
			return a["line"].(int) < b["line"].(int)
		}
		return a["column"].(int) < b["column"].(int)
	})
	return out
}

func groupRelatedReferences(locs []core.Location) []map[string]any {
	locs = append([]core.Location(nil), locs...)
	sort.Slice(locs, func(i, j int) bool {
		if locs[i].Path != locs[j].Path {
			return locs[i].Path < locs[j].Path
		}
		if locs[i].Range.Start.Line != locs[j].Range.Start.Line {
			return locs[i].Range.Start.Line < locs[j].Range.Start.Line
		}
		return locs[i].Range.Start.Column < locs[j].Range.Start.Column
	})
	out := make([]map[string]any, 0)
	for _, loc := range locs {
		if len(out) == 0 || out[len(out)-1]["path"] != loc.Path {
			out = append(out, map[string]any{"path": loc.Path, "lines": []int{}, "count": 0})
		}
		current := out[len(out)-1]
		current["count"] = current["count"].(int) + 1
		current["lines"] = append(current["lines"].([]int), loc.Range.Start.Line)
	}
	return out
}

func compactRelatedItems(value any) []map[string]any {
	items, _ := value.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		compact := make(map[string]any)
		for _, key := range []string{"symbol_id", "name", "path", "kind"} {
			if v, ok := item[key]; ok {
				compact[key] = v
			}
		}
		if span, ok := item["selection_range"].(core.Range); ok {
			compact["line"] = span.Start.Line
		}
		out = append(out, compact)
	}
	return out
}
