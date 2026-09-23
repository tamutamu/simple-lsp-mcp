package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

const (
	semanticMinBytes      = 2048
	semanticDefaultBytes  = 24576
	semanticMaxBytes      = 81920
	semanticDefaultDepth  = 1
	semanticMaxDepth      = 3
	semanticDefaultFanout = 12
	semanticMaxNodes      = 32
)

// SemanticSlice returns the smallest useful code neighborhood around one
// symbol that fits inside a caller-supplied serialized JSON byte budget. It is
// deliberately opinionated: source is included for the root and callees,
// while callers and implementations stay compact because they mostly answer
// "what depends on this?" rather than "what code must I read?".
func (e *Engine) SemanticSlice(ctx context.Context, in map[string]any) (map[string]any, error) {
	maxBytes, err := semanticByteLimit(in)
	if err != nil {
		return nil, err
	}
	depth, err := core.ClampLimit(intVal(in, "depth"), semanticMaxDepth, semanticDefaultDepth)
	if err != nil {
		return nil, err
	}
	fanout, err := core.ClampLimit(intVal(in, "limit"), min(e.MaxResults, semanticMaxNodes), semanticDefaultFanout)
	if err != nil {
		return nil, err
	}

	baseIn := copyInput(in)
	baseIn["include"] = []any{"source", "incoming_calls", "outgoing_calls", "references", "implementations"}
	baseIn["limit"] = fanout
	rootContext, err := e.SymbolContext(ctx, baseIn)
	if err != nil {
		return nil, err
	}
	if ambiguous, _ := rootContext["ambiguous"].(bool); ambiguous {
		if encodedSize(rootContext) > maxBytes {
			return nil, core.NewError(core.InvalidArgument, "ambiguous candidates exceed max_bytes; specify path or language")
		}
		return rootContext, nil
	}

	rootSymbol, _ := rootContext["symbol"].(map[string]any)
	root := copyMap(rootSymbol)
	rootSource := contextSource(rootContext)
	outgoing, _ := rootContext["outgoing_calls"].([]any)
	dependents, _ := rootContext["incoming_calls"].([]any)
	implementations := rootContext["implementations"]
	references, _ := rootContext["references"].([]any)

	complete := true
	var warnings []string
	if meta, ok := rootContext["meta"].(core.Meta); ok {
		complete = meta.Complete
		warnings = append(warnings, meta.Warnings...)
	}

	result := map[string]any{
		"root":            root,
		"dependencies":    []any{},
		"dependents":      dependents,
		"implementations": implementations,
		"related_tests":   semanticRelatedTests(references, 8),
	}

	// Type definitions help an agent understand contracts without asking it to
	// navigate separately. Missing capabilities are optional, never invented.
	typeTarget := copyInput(in)
	if id, ok := root["symbol_id"].(string); ok && id != "" {
		typeTarget = map[string]any{"symbol_id": id}
	}
	if types, err := e.Relationship(ctx, "get_type_definition", "textDocument/typeDefinition", "typeDefinition", withLimit(typeTarget, fanout)); err == nil {
		if locs, ok := types["locations"].([]core.Location); ok && len(locs) > 0 {
			result["type_definitions"] = locs
		}
	}

	// Keep at least half of the budget available for dependency source. A very
	// large root function should not crowd out the code it calls.
	if rootSource != "" {
		baseBytes := encodedSize(result)
		remaining := max(0, maxBytes-baseBytes)
		rootCap := min(remaining, maxBytes/2)
		text, truncated := truncateUTF8Bytes(rootSource, rootCap)
		root["source"] = text
		if truncated {
			warnings = append(warnings, "root source truncated to preserve semantic-slice byte budget")
		}
	}

	type queuedCall struct {
		call  map[string]any
		depth int
		via   string
	}
	queue := make([]queuedCall, 0, len(outgoing))
	rootName, _ := root["name"].(string)
	for _, raw := range outgoing {
		if call, ok := raw.(map[string]any); ok {
			queue = append(queue, queuedCall{call: call, depth: 1, via: rootName})
		}
	}

	seen := map[string]bool{}
	dependencies := make([]any, 0, min(len(queue), semanticMaxNodes))
	budgetStopped := false

	for len(queue) > 0 && len(dependencies) < semanticMaxNodes {
		q := queue[0]
		queue = queue[1:]
		key := semanticCallKey(q.call)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true

		id, _ := q.call["symbol_id"].(string)
		if id == "" {
			continue
		}
		got, err := e.GetSymbol(ctx, map[string]any{"symbol_id": id, "include_source": true})
		if err != nil {
			complete = false
			warnings = append(warnings, "dependency source: "+err.Error())
			continue
		}
		symbolMap, ok := got["symbol"].(map[string]any)
		if !ok {
			continue
		}
		dep := semanticDependency(symbolMap, q.depth, q.via)
		source, _ := symbolMap["source"].(string)
		delete(dep, "source")

		// Add metadata first, then spend whatever remains on this dependency's
		// source. Stop before emitting an object that would exceed the budget.
		probe := append(append([]any(nil), dependencies...), dep)
		result["dependencies"] = probe
		remaining := maxBytes - encodedSize(result)
		if remaining <= 0 {
			result["dependencies"] = dependencies
			budgetStopped = true
			break
		}
		if source != "" {
			text, truncated := truncateUTF8Bytes(source, remaining)
			dep["source"] = text
			if truncated {
				dep["source_truncated"] = true
			}
		}
		dependencies = append(dependencies, dep)
		result["dependencies"] = dependencies

		if q.depth >= depth {
			continue
		}
		calls, ok, warning := e.contextCalls(ctx, "get_outgoing_calls", map[string]any{"symbol_id": id}, fanout)
		if warning != "" {
			complete = false
			warnings = append(warnings, warning)
		}
		if !ok {
			complete = false
			continue
		}
		name, _ := dep["name"].(string)
		for _, raw := range calls {
			if call, ok := raw.(map[string]any); ok {
				queue = append(queue, queuedCall{call: call, depth: q.depth + 1, via: name})
			}
		}
	}
	if len(queue) > 0 && len(dependencies) >= semanticMaxNodes {
		warnings = append(warnings, fmt.Sprintf("dependency traversal capped at %d symbols", semanticMaxNodes))
		complete = false
	}
	if budgetStopped {
		warnings = append(warnings, "semantic slice stopped at max_bytes budget")
	}

	result["dependencies"] = dependencies
	meta := map[string]any{
		"complete":         complete && !budgetStopped,
		"truncated":        budgetStopped || len(queue) > 0,
		"warnings":         warnings,
		"depth":            depth,
		"dependency_count": len(dependencies),
		"max_bytes":        maxBytes,
	}
	result["meta"] = meta
	if enforceSemanticBudget(result, maxBytes) {
		meta["complete"] = false
		meta["truncated"] = true
		warnings = append(warnings, "final response compacted to max_bytes budget")
		meta["warnings"] = warnings
	}
	if deps, ok := result["dependencies"].([]any); ok {
		meta["dependency_count"] = len(deps)
	}
	// The accounting fields themselves add a few bytes. A second pass keeps
	// the serialized result bounded even after meta is finalized.
	if enforceSemanticBudget(result, maxBytes) {
		meta["complete"] = false
		meta["truncated"] = true
	}
	if encodedSize(result) > maxBytes {
		return nil, core.NewError(core.InvalidArgument, "max_bytes cannot fit the root identity; increase max_bytes or narrow the symbol")
	}
	return result, nil
}

// semanticRelatedTests returns only files that the LSP says reference the
// symbol and whose names conventionally identify tests. It is a candidate
// list, not a claim that a particular test case covers the change.
func semanticRelatedTests(references []any, limit int) []any {
	tests := make([]any, 0)
	for _, raw := range references {
		ref, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		path, _ := ref["path"].(string)
		if !semanticTestFile(path) {
			continue
		}
		tests = append(tests, map[string]any{"path": path, "reference_lines": ref["lines"], "reason": "symbol_reference"})
		if len(tests) >= limit {
			break
		}
	}
	return tests
}

func semanticTestFile(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	return strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".test.ts") ||
		strings.HasSuffix(name, ".test.tsx") || strings.HasSuffix(name, ".spec.ts") ||
		strings.HasSuffix(name, ".spec.tsx") || strings.HasSuffix(name, ".test.js") ||
		strings.HasSuffix(name, ".spec.js") || (strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py")) ||
		strings.HasSuffix(name, "_test.py")
}

// enforceSemanticBudget progressively removes the least valuable payload
// until the serialized response fits inside maxBytes. It preserves root
// identity before dependency source, dependency nodes, callers, and
// implementation locations, in that order.
func enforceSemanticBudget(result map[string]any, maxBytes int) bool {
	if encodedSize(result) <= maxBytes {
		return false
	}
	changed := false
	if deps, ok := result["dependencies"].([]any); ok {
		for i := len(deps) - 1; i >= 0 && encodedSize(result) > maxBytes; i-- {
			dep, ok := deps[i].(map[string]any)
			if !ok {
				continue
			}
			if _, exists := dep["source"]; exists {
				delete(dep, "source")
				dep["source_truncated"] = true
				changed = true
			}
		}
		for len(deps) > 0 && encodedSize(result) > maxBytes {
			deps = deps[:len(deps)-1]
			result["dependencies"] = deps
			changed = true
		}
	}
	if encodedSize(result) > maxBytes {
		if root, ok := result["root"].(map[string]any); ok {
			if source, ok := root["source"].(string); ok {
				delete(root, "source")
				root["source_truncated"] = true
				if encodedSize(result) < maxBytes {
					root["source"] = fitSourceToEncodedBudget(result, root, source, maxBytes)
				}
				changed = true
			}
		}
	}
	if dependents, ok := result["dependents"].([]any); ok {
		for len(dependents) > 0 && encodedSize(result) > maxBytes {
			dependents = dependents[:len(dependents)-1]
			result["dependents"] = dependents
			changed = true
		}
	}
	if implementations, ok := result["implementations"].([]core.Location); ok {
		for len(implementations) > 0 && encodedSize(result) > maxBytes {
			implementations = implementations[:len(implementations)-1]
			result["implementations"] = implementations
			changed = true
		}
	}
	if tests, ok := result["related_tests"].([]any); ok {
		for len(tests) > 0 && encodedSize(result) > maxBytes {
			tests = tests[:len(tests)-1]
			result["related_tests"] = tests
			changed = true
		}
	}
	if types, ok := result["type_definitions"].([]core.Location); ok {
		for len(types) > 0 && encodedSize(result) > maxBytes {
			types = types[:len(types)-1]
			result["type_definitions"] = types
			changed = true
		}
	}
	if encodedSize(result) > maxBytes {
		if meta, ok := result["meta"].(map[string]any); ok {
			delete(meta, "warnings")
			changed = true
		}
	}
	return changed
}

func fitSourceToEncodedBudget(result map[string]any, holder map[string]any, source string, maxBytes int) string {
	runes := []rune(source)
	lo, hi := 0, len(runes)
	best := ""
	for lo <= hi {
		mid := lo + (hi-lo)/2
		candidate := string(runes[:mid])
		holder["source"] = candidate
		if encodedSize(result) <= maxBytes {
			best = candidate
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	holder["source"] = best
	return best
}

func semanticByteLimit(in map[string]any) (int, error) {
	if _, deprecated := in["max_tokens"]; deprecated {
		return 0, core.NewError(core.InvalidArgument, "max_tokens has been removed; specify max_bytes instead")
	}
	value := intVal(in, "max_bytes")
	if value == 0 {
		return semanticDefaultBytes, nil
	}
	if value < semanticMinBytes || value > semanticMaxBytes {
		return 0, core.NewError(core.InvalidArgument, fmt.Sprintf("max_bytes must be between %d and %d", semanticMinBytes, semanticMaxBytes))
	}
	return value, nil
}

func contextSource(v map[string]any) string {
	source, _ := v["source"].(map[string]any)
	text, _ := source["text"].(string)
	return text
}

func semanticDependency(symbol map[string]any, depth int, via string) map[string]any {
	out := map[string]any{"depth": depth}
	if via != "" {
		out["via"] = via
	}
	for _, key := range []string{"symbol_id", "symbol_path", "name", "kind", "container_name", "language", "path", "range", "selection_range", "source"} {
		if value, ok := symbol[key]; ok {
			out[key] = value
		}
	}
	return out
}

func semanticCallKey(call map[string]any) string {
	path, _ := call["path"].(string)
	name, _ := call["name"].(string)
	line := call["line"]
	if path == "" || name == "" {
		return ""
	}
	return fmt.Sprintf("%s#%v:%s", path, line, name)
}

func copyInput(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func encodedSize(v any) int {
	b, _ := json.Marshal(v)
	return len(b)
}

func truncateUTF8Bytes(s string, maxBytes int) (string, bool) {
	if maxBytes < 0 {
		maxBytes = 0
	}
	if len(s) <= maxBytes {
		return s, false
	}
	end := min(len(s), maxBytes)
	for end > 0 && !utf8.ValidString(s[:end]) {
		end--
	}
	return s[:end], true
}
