package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

const (
	semanticMinTokens     = 512
	semanticDefaultTokens = 6000
	semanticMaxTokens     = 20000
	semanticDefaultDepth  = 1
	semanticMaxDepth      = 3
	semanticDefaultFanout = 12
	semanticMaxNodes      = 32
)

// SemanticSlice returns the smallest useful code neighborhood around one
// symbol that fits inside a caller-supplied approximate token budget. It is
// deliberately opinionated: source is included for the root and callees,
// while callers and implementations stay compact because they mostly answer
// "what depends on this?" rather than "what code must I read?".
func (e *Engine) SemanticSlice(ctx context.Context, in map[string]any) (map[string]any, error) {
	maxTokens, err := semanticTokenLimit(in)
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
	baseIn["include"] = []any{"source", "incoming_calls", "outgoing_calls", "implementations"}
	baseIn["limit"] = fanout
	rootContext, err := e.SymbolContext(ctx, baseIn)
	if err != nil {
		return nil, err
	}
	if ambiguous, _ := rootContext["ambiguous"].(bool); ambiguous {
		return rootContext, nil
	}

	rootSymbol, _ := rootContext["symbol"].(map[string]any)
	root := copyMap(rootSymbol)
	rootSource := contextSource(rootContext)
	outgoing, _ := rootContext["outgoing_calls"].([]any)
	dependents, _ := rootContext["incoming_calls"].([]any)
	implementations := rootContext["implementations"]

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
	}
	maxBytes := maxTokens * 4

	// Keep at least half of the budget available for dependency source. A very
	// large root function should not crowd out the code it calls.
	if rootSource != "" {
		baseBytes := encodedSize(result)
		remaining := max(0, maxBytes-baseBytes)
		rootCap := min(remaining, maxBytes/2)
		text, truncated := truncateUTF8Bytes(rootSource, rootCap)
		root["source"] = text
		if truncated {
			warnings = append(warnings, "root source truncated to preserve semantic-slice token budget")
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
		if !ok {
			complete = false
			warnings = append(warnings, warning)
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
		warnings = append(warnings, "semantic slice stopped at max_tokens budget")
	}

	result["dependencies"] = dependencies
	meta := map[string]any{
		"complete":         complete && !budgetStopped,
		"truncated":        budgetStopped || len(queue) > 0,
		"warnings":         warnings,
		"depth":            depth,
		"dependency_count": len(dependencies),
		"max_tokens":       maxTokens,
	}
	result["meta"] = meta
	if enforceSemanticBudget(result, maxBytes) {
		meta["complete"] = false
		meta["truncated"] = true
		warnings = append(warnings, "final response compacted to max_tokens budget")
		meta["warnings"] = warnings
	}
	if deps, ok := result["dependencies"].([]any); ok {
		meta["dependency_count"] = len(deps)
	}
	meta["estimated_tokens"] = estimatedTokens(result)
	// The accounting fields themselves add a few bytes. A second pass keeps
	// the serialized result bounded even after meta is finalized.
	if enforceSemanticBudget(result, maxBytes) {
		meta["complete"] = false
		meta["truncated"] = true
	}
	meta["estimated_tokens"] = estimatedTokens(result)
	return result, nil
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

func semanticTokenLimit(in map[string]any) (int, error) {
	value := intVal(in, "max_tokens")
	if value == 0 {
		return semanticDefaultTokens, nil
	}
	if value < semanticMinTokens || value > semanticMaxTokens {
		return 0, core.NewError(core.InvalidArgument, fmt.Sprintf("max_tokens must be between %d and %d", semanticMinTokens, semanticMaxTokens))
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

func estimatedTokens(v any) int {
	return (encodedSize(v) + 3) / 4
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
