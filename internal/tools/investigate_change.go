package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
)

const (
	defaultInvestigationDepth = 2
	defaultInvestigationNodes = 40
)

// InvestigateChange is an LSP-backed, one-call answer to "what uses this
// declaration, and what might be affected?". It combines exact symbol
// resolution and transitive semantic reference traversal. The default files
// view is bounded and grouped by path to avoid returning huge repeated
// ranges to coding agents. This never writes source and never claims to
// predict dynamic behavior.
func (e *Engine) InvestigateChange(ctx context.Context, in map[string]any) (map[string]any, error) {
	if raw, present := in["targets"]; present {
		return e.investigateChangesBatch(ctx, in, raw)
	}
	normalized, positionHint, validateErr := normalizedInvestigationTarget(in)
	if validateErr != nil {
		return nil, validateErr
	}
	detail := stringVal(in, "detail")
	if detail == "" {
		detail = "files"
	}
	if detail != "files" && detail != "graph" {
		return nil, core.NewError(core.InvalidArgument, "detail must be files or graph")
	}
	depth, nodes := intVal(in, "depth"), intVal(in, "max_nodes")
	if depth == 0 {
		depth = defaultInvestigationDepth
	}
	if nodes == 0 {
		nodes = defaultInvestigationNodes
	}
	if depth < 1 || depth > maxImpactDepth || nodes < 1 || nodes > maxImpactNodes {
		return nil, core.NewError(core.InvalidArgument, "depth must be 1..16 and max_nodes must be 1..200")
	}
	// Known file + symbol_path is the common coding-agent case. Resolve the
	// LSP symbol tree just once and pass the same snapshot to traversal.
	var found map[string]any
	var selected symbolNode
	var initial impactDocument
	var err error
	knownPath := stringVal(in, "path")
	knownSymbol := stringVal(in, "symbol_path")
	if knownPath != "" && knownSymbol != "" && stringVal(in, "symbol_id") == "" {
		var profile language.Profile
		profile, err = e.profileForInput(in)
		if err != nil {
			return nil, err
		}
		server, doc, allNodes, loadErr := e.documentNodes(ctx, profile, knownPath)
		if loadErr != nil {
			return nil, loadErr
		}
		selected, err = resolveInspectNodeByPath(allNodes, knownPath, knownSymbol)
		if err != nil {
			return nil, err
		}
		found, err = e.renderInspected(doc, selected, "location", defaultSymbolSourceLines)
		if err != nil {
			return nil, err
		}
		initial = impactDocument{Profile: profile, Session: server, Document: doc, Nodes: allNodes}
	} else {
		target := make(map[string]any, 7)
		for _, key := range []string{"symbol_id", "symbol_path", "path", "line", "column", "language"} {
			if value, ok := normalized[key]; ok {
				target[key] = value
			}
		}
		target["view"] = "location"
		found, err = e.InspectSymbol(ctx, target)
		if err != nil {
			return nil, err
		}
		path, _ := found["path"].(string)
		var profile language.Profile
		profile, err = language.FromPath(path)
		if err != nil {
			return nil, err
		}
		server, doc, allNodes, loadErr := e.documentNodes(ctx, profile, path)
		if loadErr != nil {
			return nil, loadErr
		}
		selection, ok := found["selection_range"].(core.Range)
		if !ok {
			return nil, core.NewError(core.IncompleteSearch, "LSP did not provide an exact declaration position")
		}
		selected, err = impactSymbolOnPosition(allNodes, selection.Start)
		if err != nil {
			return nil, err
		}
		initial = impactDocument{Profile: profile, Session: server, Document: doc, Nodes: allNodes}
	}
	resolvedPath, _ := found["path"].(string)
	if resolvedPath == "" || selected.SelectionRange.Start.Line < 1 || selected.SelectionRange.Start.Column < 1 {
		return nil, core.NewError(core.IncompleteSearch, "LSP did not provide an exact declaration position")
	}
	if positionHint.Line > 0 && selected.SelectionRange.Start != positionHint {
		return nil, core.NewError(core.InvalidArgument,
			fmt.Sprintf("declaration %s is at %d:%d, not the supplied %d:%d",
				knownSymbol, selected.SelectionRange.Start.Line, selected.SelectionRange.Start.Column,
				positionHint.Line, positionHint.Column))
	}
	graph, err := e.analyzeImpactResolved(ctx, impactSymbol{Path: resolvedPath, Node: selected}, initial, depth, nodes)
	if err != nil {
		return nil, err
	}
	return summarizeChangeInvestigation(found, graph, detail)
}

// normalizedInvestigationTarget allows a coding agent to pass both a known
// symbol path and its source position. Those coordinates corroborate the
// symbol's identity, rather than choosing a competing resolution mode.
// Wrong/stale coordinates are rejected after the LSP resolves the declaration.
func normalizedInvestigationTarget(in map[string]any) (map[string]any, core.Position, error) {
	clean := make(map[string]any, len(in))
	for key, value := range in {
		clean[key] = value
	}
	var hint core.Position
	_, lineSupplied := in["line"]
	_, columnSupplied := in["column"]
	if stringVal(in, "path") != "" && stringVal(in, "symbol_path") != "" &&
		stringVal(in, "symbol_id") == "" && (lineSupplied || columnSupplied) {
		line, column := intVal(in, "line"), intVal(in, "column")
		if !lineSupplied || !columnSupplied || line < 1 || column < 1 {
			return nil, hint, core.NewError(core.InvalidArgument, "symbol path coordinates must include positive line and column")
		}
		hint = core.Position{Line: line, Column: column}
		delete(clean, "line")
		delete(clean, "column")
	}
	if err := targetOf(clean).Validate(); err != nil {
		return nil, hint, err
	}
	return clean, hint, nil
}

type investigationDeclaration struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Line  int    `json:"line"`
	Depth int    `json:"depth"`
}

type investigationAffectedFile struct {
	Path         string                     `json:"path"`
	Count        int                        `json:"count"`
	Declarations []investigationDeclaration `json:"declarations"`
}

// investigateChangesBatch allows a long-lived agent to inspect 2-6 concrete
// declarations in ONE tool turn. It preserves each independent LSP graph's
// completeness metadata rather than asserting that merging paths proves a
// merged program dependency graph. All requests are validated first.
func (e *Engine) investigateChangesBatch(ctx context.Context, in map[string]any, raw any) (map[string]any, error) {
	items, ok := raw.([]any)
	if !ok || len(items) < 2 || len(items) > 6 {
		return nil, core.NewError(core.InvalidArgument, "targets must be an array of 2 to 6 distinct LSP declarations")
	}
	for _, name := range []string{"symbol_id", "symbol_path", "path", "line", "column", "language"} {
		if _, present := in[name]; present {
			return nil, core.NewError(core.InvalidArgument, "batch targets cannot be combined with "+name)
		}
	}
	validated := make([]map[string]any, 0, len(items))
	seen := make(map[string]bool, len(items))
	for i, item := range items {
		target, ok := item.(map[string]any)
		if !ok {
			return nil, core.NewError(core.InvalidArgument, fmt.Sprintf("targets[%d] must be an object", i))
		}
		for key := range target {
			switch key {
			case "symbol_id", "symbol_path", "path", "line", "column", "language":
			default:
				return nil, core.NewError(core.InvalidArgument, fmt.Sprintf("unknown field %s in targets[%d]", key, i))
			}
		}
		normalized, _, err := normalizedInvestigationTarget(target)
		if err != nil {
			return nil, core.NewError(core.InvalidArgument, fmt.Sprintf("targets[%d]: %v", i, err))
		}
		identity, _ := json.Marshal(normalized)
		if seen[string(identity)] {
			return nil, core.NewError(core.InvalidArgument, fmt.Sprintf("duplicate target at index %d", i))
		}
		seen[string(identity)] = true
		validated = append(validated, target)
	}
	shared := make(map[string]any, len(in))
	for _, key := range []string{"depth", "max_nodes", "detail"} {
		if value, ok := in[key]; ok {
			shared[key] = value
		}
	}
	batchDetail := stringVal(shared, "detail")
	if batchDetail != "" && batchDetail != "files" && batchDetail != "union" {
		// Large graph payloads belong in individual detail=graph requests.
		return nil, core.NewError(core.InvalidArgument, "batched investigation supports detail=files or union")
	}
	// The actual semantic query is identical for detailed and compact batch
	// results; only returned evidence is condensed for agent context.
	shared["detail"] = "files"
	results := make([]map[string]any, 0, len(items))
	uniquePaths := make(map[string]bool)
	complete := true
	for i, target := range validated {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sub := make(map[string]any, len(target)+len(shared))
		for key, value := range shared {
			sub[key] = value
		}
		for key, value := range target {
			sub[key] = value
		}
		result, err := e.InvestigateChange(ctx, sub)
		if err != nil {
			// Never silently omit one of the requested declarations.
			return nil, core.WithCause(core.IncompleteSearch, fmt.Sprintf("targets[%d] investigation failed", i), err)
		}
		meta, _ := result["meta"].(map[string]any)
		complete = complete && metaComplete(meta)
		files, ok := result["affected_files"].([]investigationAffectedFile)
		if !ok {
			return nil, core.NewError(core.InternalError, "unexpected affected-file record type")
		}
		perTarget := make([]string, 0, len(files))
		for _, file := range files {
			uniquePaths[file.Path] = true
			perTarget = append(perTarget, file.Path)
		}
		if batchDetail == "union" {
			results = append(results, map[string]any{
				"target": result["target"], "affected_files": perTarget,
				"affected_file_count":       result["affected_file_count"],
				"affected_declarations":     result["affected_declarations"],
				"direct_reference_count":    result["direct_reference_count"],
				"traversed_reference_count": result["traversed_reference_count"],
				"unmapped_references":       result["unmapped_references"],
				"depth":                     result["depth"], "meta": meta,
				"incomplete": !metaComplete(meta),
			})
		} else {
			results = append(results, result)
		}
	}
	paths := make([]string, 0, len(uniquePaths))
	for path := range uniquePaths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	answer := map[string]any{
		"results": results, "targets_count": len(validated),
		"affected_files_union": paths, "affected_file_count": len(paths),
		"meta": map[string]any{"complete": complete, "incomplete": !complete, "note": "Each result is a separate bounded LSP impact graph; the path union does not imply a combined dependency graph."},
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 64*1024 {
		return nil, core.NewError(core.IncompleteSearch, "batched investigation exceeds 64 KiB; use fewer targets")
	}
	return answer, nil
}

func summarizeChangeInvestigation(found, graph map[string]any, detail string) (map[string]any, error) {
	nodes, okNodes := graph["nodes"].([]impactNode)
	edges, okEdges := graph["edges"].([]impactEdge)
	if !okNodes || !okEdges || len(nodes) < 1 {
		return nil, core.NewError(core.InternalError, "LSP impact graph lacks its root or exact node/edge records")
	}
	rootID, ok := graph["root"].(string)
	if !ok || rootID != nodes[0].ID {
		return nil, core.NewError(core.InternalError, "LSP impact root is inconsistent")
	}
	grouped := map[string]*investigationAffectedFile{}
	for _, n := range nodes[1:] {
		file := grouped[n.Path]
		if file == nil {
			file = &investigationAffectedFile{Path: n.Path}
			grouped[n.Path] = file
		}
		file.Count++
		file.Declarations = append(file.Declarations, investigationDeclaration{
			Name: n.Name, Kind: n.Kind, Line: n.Line, Depth: n.Depth,
		})
	}
	paths := make([]string, 0, len(grouped))
	for path := range grouped {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	files := make([]investigationAffectedFile, 0, len(paths))
	for _, path := range paths {
		file := grouped[path]
		sort.Slice(file.Declarations, func(i, j int) bool {
			a, b := file.Declarations[i], file.Declarations[j]
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			return a.Name < b.Name
		})
		files = append(files, *file)
	}
	direct, totalReferences := 0, 0
	for _, edge := range edges {
		totalReferences += edge.ReferenceCount
		if edge.From == rootID {
			direct += edge.ReferenceCount
		}
	}
	meta, ok := graph["meta"].(map[string]any)
	if !ok {
		return nil, core.NewError(core.InternalError, "LSP impact graph lacks completeness metadata")
	}
	result := map[string]any{
		"target": map[string]any{
			"path": found["path"], "name": found["name"], "kind": found["kind"],
			"line": nodes[0].Line,
		},
		"affected_files": files, "affected_file_count": len(files),
		"affected_declarations":  len(nodes) - 1,
		"direct_reference_count": direct, "traversed_reference_count": totalReferences,
		"unmapped_references": graph["unmapped_references"],
		"depth":               graph["depth"], "meta": meta, "incomplete": !metaComplete(meta),
	}
	if detail == "graph" {
		result["graph"] = map[string]any{"root": rootID, "nodes": nodes, "edges": edges}
	}
	if len(files) == 0 && !metaComplete(meta) {
		// An empty affected-file set is not a supported "no impact" answer
		// when traversal failed or stopped at a boundary.
		result["warning"] = fmt.Sprintf("incomplete LSP traversal (%v)", meta["limit_reasons"])
	}
	return result, nil
}

func metaComplete(meta map[string]any) bool {
	complete, _ := meta["complete"].(bool)
	return complete
}
