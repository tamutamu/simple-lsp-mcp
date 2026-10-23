package tools

import (
	"context"
	"fmt"
	"sort"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
)

const verifyMaxSemanticSymbols = 32

type semanticRelation struct {
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	SymbolPath string `json:"symbol_path,omitempty"`
	Name       string `json:"name,omitempty"`
	Count      int    `json:"count"`
}

type semanticRelationDelta struct {
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	SymbolPath    string `json:"symbol_path,omitempty"`
	Name          string `json:"name,omitempty"`
	BeforeCount   int    `json:"before_count"`
	AfterCount    int    `json:"after_count"`
	TargetChanged bool   `json:"target_changed"`
}

type relationSnapshot struct {
	Relations map[string]semanticRelation
	Complete  bool
	Warnings  []string
}

func (e *Engine) semanticRelationDiff(ctx context.Context, base *Engine, changes gitChangeSet, changedSymbols []changedSemanticSymbol, changedPaths map[string]bool, limit int) (map[string]any, []verifyFinding, []string, bool) {
	out := map[string]any{
		"baseline_checked": true,
		"complete":         true,
		"symbols":          []any{},
		"compared_count":   0,
		"unpaired_count":   0,
		"removed_count":    0,
		"added_count":      0,
	}
	if len(changedSymbols) == 0 {
		out["baseline_checked"] = false
		return out, nil, nil, true
	}

	changeByPath := make(map[string]changedFile, len(changes.Files))
	for _, file := range changes.Files {
		changeByPath[file.Path] = file
	}

	findings := make([]verifyFinding, 0)
	warnings := make([]string, 0)
	symbolResults := make([]any, 0, len(changedSymbols))
	removedTotal, addedTotal := 0, 0
	comparedTotal, unpairedTotal := 0, 0
	complete := true

	for _, changed := range changedSymbols {
		file, ok := changeByPath[changed.Path]
		if !ok || file.Untracked || file.BasePath == "" {
			unpairedTotal++
			symbolResults = append(symbolResults, map[string]any{
				"path": changed.Path, "symbol_path": changed.Node.SymbolPath, "status": "new_or_unpaired",
				"removed": []semanticRelationDelta{}, "added": []semanticRelationDelta{},
			})
			continue
		}

		baseSymbol, pairedBy, pairWarnings, pairOK := base.matchBaseSymbol(ctx, changed, file)
		warnings = append(warnings, pairWarnings...)
		if !pairOK {
			unpairedTotal++
			symbolResults = append(symbolResults, map[string]any{
				"path": changed.Path, "symbol_path": changed.Node.SymbolPath, "base_path": file.BasePath,
				"status": "unpaired", "removed": []semanticRelationDelta{}, "added": []semanticRelationDelta{},
			})
			continue
		}
		if comparedTotal >= verifyMaxSemanticSymbols {
			warnings = append(warnings, fmt.Sprintf("semantic relation comparison capped at %d paired changed symbols", verifyMaxSemanticSymbols))
			complete = false
			break
		}
		comparedTotal++

		before := base.semanticRelations(ctx, baseSymbol, limit)
		after := e.semanticRelations(ctx, changed, limit)
		warnings = append(warnings, prefixWarnings("baseline "+file.BasePath+"#"+baseSymbol.Node.SymbolPath, before.Warnings)...)
		warnings = append(warnings, prefixWarnings("current "+changed.Path+"#"+changed.Node.SymbolPath, after.Warnings)...)
		if !before.Complete || !after.Complete {
			complete = false
		}

		removed, added := diffSemanticRelations(before.Relations, after.Relations, changedPaths)
		removedTotal += len(removed)
		addedTotal += len(added)
		for _, relation := range removed {
			if relation.TargetChanged || !reviewWorthyRemovedRelation(relation.Kind) {
				continue
			}
			findings = append(findings, verifyFinding{
				Severity: "warning", Code: "SEMANTIC_RELATION_REMOVED", Path: relation.Path, Symbol: changed.Node.SymbolPath,
				Message: fmt.Sprintf("%s relation disappeared (%d -> %d) while the related file was not changed", relation.Kind, relation.BeforeCount, relation.AfterCount),
			})
		}
		symbolResults = append(symbolResults, map[string]any{
			"path": changed.Path, "symbol_path": changed.Node.SymbolPath,
			"base_path": file.BasePath, "base_symbol_path": baseSymbol.Node.SymbolPath,
			"paired_by": pairedBy, "status": "compared",
			"removed": removed, "added": added,
			"complete": before.Complete && after.Complete,
		})
	}

	out["symbols"] = symbolResults
	out["compared_count"] = comparedTotal
	out["unpaired_count"] = unpairedTotal
	out["removed_count"] = removedTotal
	out["added_count"] = addedTotal
	out["complete"] = complete
	return out, findings, warnings, complete
}

func (e *Engine) matchBaseSymbol(ctx context.Context, current changedSemanticSymbol, file changedFile) (changedSemanticSymbol, string, []string, bool) {
	p, profileErr := language.FromPath(file.BasePath)
	if profileErr != nil || !e.Sessions.Configured(p.SessionKey) {
		return changedSemanticSymbol{}, "", nil, false
	}
	_, d, nodes, err := e.documentNodes(ctx, p, file.BasePath)
	if err != nil {
		return changedSemanticSymbol{}, "", []string{"could not read baseline symbols in " + file.BasePath + ": " + err.Error()}, false
	}
	if node, ok := findNodeBySymbolPath(nodes, current.Node.SymbolPath, current.Node.Kind); ok {
		return changedSemanticSymbol{
			Path: file.BasePath, Profile: p, Node: node,
			SymbolID: e.registerNode(p, file.BasePath, d.URI, d.Hash, node),
		}, "symbol_path", nil, true
	}

	// Do not guess a pairing from a nearby changed hunk. Insertions, deletions,
	// and renames can place unrelated symbols at the same line in the two
	// revisions. A false semantic diff is worse than an explicit unpaired
	// result, so v1 compares only symbols with the same stable symbol_path.
	return changedSemanticSymbol{}, "", nil, false
}

func findNodeBySymbolPath(nodes []symbolNode, symbolPath, kind string) (symbolNode, bool) {
	for _, node := range nodes {
		if node.SymbolPath == symbolPath && (kind == "" || node.Kind == kind) {
			return node, true
		}
		if found, ok := findNodeBySymbolPath(node.Children, symbolPath, kind); ok {
			return found, true
		}
	}
	return symbolNode{}, false
}

func (e *Engine) semanticRelations(ctx context.Context, changed changedSemanticSymbol, limit int) relationSnapshot {
	_, session, _, _, targetErr := e.target(ctx, map[string]any{"symbol_id": changed.SymbolID})
	if targetErr != nil {
		return relationSnapshot{Relations: map[string]semanticRelation{}, Complete: false, Warnings: []string{targetErr.Error()}}
	}
	caps := session.Capabilities()
	include := make([]any, 0, 4)
	if caps.References {
		include = append(include, "references")
	}
	if callableSymbolKind(changed.Node.Kind) && caps.CallHierarchy {
		include = append(include, "incoming_calls", "outgoing_calls")
	}
	if typeLikeSymbolKind(changed.Node.Kind) && caps.Implementation {
		include = append(include, "implementations")
	}
	if len(include) == 0 {
		return relationSnapshot{Relations: map[string]semanticRelation{}, Complete: true}
	}
	result, err := e.SymbolContext(ctx, map[string]any{
		"symbol_id": changed.SymbolID, "include": include, "limit": limit,
	})
	if err != nil {
		return relationSnapshot{Relations: map[string]semanticRelation{}, Complete: false, Warnings: []string{err.Error()}}
	}
	complete := true
	warnings := []string{}
	if meta, ok := result["meta"].(core.Meta); ok {
		complete = meta.Complete && !meta.Truncated
		warnings = append(warnings, meta.Warnings...)
	}
	return relationSnapshot{Relations: collectSemanticRelations(result), Complete: complete, Warnings: warnings}
}

func collectSemanticRelations(result map[string]any) map[string]semanticRelation {
	out := map[string]semanticRelation{}
	for _, section := range []struct {
		Name string
		Kind string
	}{{"incoming_calls", "incoming_call"}, {"outgoing_calls", "outgoing_call"}} {
		calls, _ := result[section.Name].([]any)
		for _, raw := range calls {
			entry, _ := raw.(map[string]any)
			path, _ := entry["path"].(string)
			if path == "" {
				continue
			}
			symbolPath, _ := entry["symbol_path"].(string)
			name, _ := entry["name"].(string)
			key := relationKey(section.Kind, path, symbolPath, name)
			rel := out[key]
			rel.Kind, rel.Path, rel.SymbolPath, rel.Name = section.Kind, path, symbolPath, name
			rel.Count++
			out[key] = rel
		}
	}
	refs, _ := result["references"].([]any)
	for _, raw := range refs {
		entry, _ := raw.(map[string]any)
		path, _ := entry["path"].(string)
		if path == "" {
			continue
		}
		count := 1
		switch v := entry["count"].(type) {
		case int:
			count = v
		case float64:
			count = int(v)
		}
		key := relationKey("reference", path, "", "")
		out[key] = semanticRelation{Kind: "reference", Path: path, Count: count}
	}
	impls, _ := result["implementations"].([]core.Location)
	for _, loc := range impls {
		key := relationKey("implementation", loc.Path, "", "")
		rel := out[key]
		rel.Kind, rel.Path = "implementation", loc.Path
		rel.Count++
		out[key] = rel
	}
	return out
}

func relationKey(kind, path, symbolPath, name string) string {
	return kind + "\x00" + path + "\x00" + symbolPath + "\x00" + name
}

func diffSemanticRelations(before, after map[string]semanticRelation, changedPaths map[string]bool) ([]semanticRelationDelta, []semanticRelationDelta) {
	removed := make([]semanticRelationDelta, 0)
	added := make([]semanticRelationDelta, 0)
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		b, bok := before[key]
		a, aok := after[key]
		beforeCount, afterCount := 0, 0
		if bok {
			beforeCount = b.Count
		}
		if aok {
			afterCount = a.Count
		}
		if beforeCount == afterCount {
			continue
		}
		rel := b
		if !bok {
			rel = a
		}
		delta := semanticRelationDelta{
			Kind: rel.Kind, Path: rel.Path, SymbolPath: rel.SymbolPath, Name: rel.Name,
			BeforeCount: beforeCount, AfterCount: afterCount, TargetChanged: changedPaths[rel.Path],
		}
		if beforeCount > afterCount {
			removed = append(removed, delta)
		} else {
			added = append(added, delta)
		}
	}
	return removed, added
}

func reviewWorthyRemovedRelation(kind string) bool {
	switch kind {
	case "incoming_call", "outgoing_call", "implementation":
		return true
	default:
		return false
	}
}

func prefixWarnings(prefix string, warnings []string) []string {
	out := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if warning != "" {
			out = append(out, prefix+": "+warning)
		}
	}
	return out
}
