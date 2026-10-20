package tools

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
)

const verifyMaxDiagnosticFiles = 32

type verifyFinding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
	Message  string `json:"message"`
}

type changedSemanticSymbol struct {
	Path               string
	Profile            language.Profile
	Node               symbolNode
	SymbolID           string
	ChangeKind         string
	DeclarationChanged bool
}

type affectedSummary struct {
	Reasons []string
	Symbols map[string]bool
}

// VerifyChange is the Semantic Change Guard entry point. It maps the Git
// working-tree diff to current LSP symbols, asks the existing semantic graph
// for their impact, then checks for suspicious untouched dependencies and
// current LSP errors. It is intentionally read-only.
func (e *Engine) VerifyChange(ctx context.Context, in map[string]any) (map[string]any, error) {
	depth, err := core.ClampLimit(intVal(in, "depth"), impactMaxDepth, 1)
	if err != nil {
		return nil, err
	}
	limit, err := e.limit(in)
	if err != nil {
		return nil, err
	}
	changes, err := e.gitChanges(ctx, stringVal(in, "base"))
	if err != nil {
		return nil, err
	}

	changedPaths := map[string]bool{}
	changedRanges := map[string][]changedLineRange{}
	fileOutput := make([]any, 0, len(changes.Files))
	warnings := []string{}
	complete := true
	for _, file := range changes.Files {
		changedPaths[file.Path] = true
		changedRanges[file.Path] = file.Ranges
		entry := map[string]any{"path": file.Path, "ranges": file.Ranges}
		if file.Deleted {
			entry["deleted"] = true
			warnings = append(warnings, "deleted file cannot be mapped to current LSP symbols: "+file.Path)
			complete = false
		}
		if file.Untracked {
			entry["untracked"] = true
		}
		fileOutput = append(fileOutput, entry)
	}

	changedSymbols := make([]changedSemanticSymbol, 0)
	seenSymbols := map[string]bool{}
	for _, file := range changes.Files {
		if file.Deleted || len(file.Ranges) == 0 {
			continue
		}
		p, profileErr := language.FromPath(file.Path)
		if profileErr != nil || !e.Sessions.Configured(p.SessionKey) {
			continue
		}
		s, d, nodes, nodeErr := e.documentNodes(ctx, p, file.Path)
		if nodeErr != nil {
			warnings = append(warnings, "could not map changed lines to symbols in "+file.Path+": "+nodeErr.Error())
			complete = false
			continue
		}
		_ = s
		for _, node := range nodesForChangedRanges(nodes, file.Ranges) {
			key := file.Path + "#" + node.SymbolPath
			if seenSymbols[key] {
				continue
			}
			seenSymbols[key] = true
			declarationChanged := nodeDeclarationChanged(node, file.Ranges)
			kind := "body_change"
			if declarationChanged {
				kind = "declaration_change"
			}
			changedSymbols = append(changedSymbols, changedSemanticSymbol{
				Path: file.Path, Profile: p, Node: node,
				SymbolID:   e.registerNode(p, file.Path, d.URI, d.Hash, node),
				ChangeKind: kind, DeclarationChanged: declarationChanged,
			})
		}
	}
	sort.Slice(changedSymbols, func(i, j int) bool {
		if changedSymbols[i].Path != changedSymbols[j].Path {
			return changedSymbols[i].Path < changedSymbols[j].Path
		}
		return changedSymbols[i].Node.SelectionRange.Start.Line < changedSymbols[j].Node.SelectionRange.Start.Line
	})

	affected := map[string]*affectedSummary{}
	declarationAffected := map[string]bool{}
	for _, changed := range changedSymbols {
		entries, semanticWarnings := e.verifyAffectedFiles(ctx, changed, depth, limit)
		if len(semanticWarnings) > 0 {
			warnings = append(warnings, semanticWarnings...)
			complete = false
		}
		for path, reasons := range entries {
			if path == "" || path == changed.Path {
				continue
			}
			summary := affected[path]
			if summary == nil {
				summary = &affectedSummary{Symbols: map[string]bool{}}
				affected[path] = summary
			}
			summary.Reasons = mergeStrings(summary.Reasons, reasons...)
			summary.Symbols[changed.Node.SymbolPath] = true
			if changed.DeclarationChanged {
				declarationAffected[path] = true
			}
		}
	}

	findings := []verifyFinding{}
	relatedTests := []string{}
	affectedOutput := make([]any, 0, len(affected))
	affectedPaths := make([]string, 0, len(affected))
	for path := range affected {
		affectedPaths = append(affectedPaths, path)
	}
	sort.Strings(affectedPaths)
	for _, path := range affectedPaths {
		summary := affected[path]
		symbols := sortedKeys(summary.Symbols)
		isTest := isTestSourcePath(path)
		if isTest {
			relatedTests = append(relatedTests, path)
		}
		affectedOutput = append(affectedOutput, map[string]any{
			"path": path, "reasons": summary.Reasons, "symbols": symbols,
			"changed": changedPaths[path], "related_test": isTest,
		})
		if declarationAffected[path] && !changedPaths[path] && !isTest {
			findings = append(findings, verifyFinding{
				Severity: "warning", Code: "AFFECTED_FILE_NOT_CHANGED", Path: path,
				Message: "a changed declaration semantically affects this file, but the file is not in the current diff",
			})
		}
	}

	// Diagnostics are checked on every changed source file and on untouched
	// files affected by a declaration change. Errors on those files make the
	// current change unsafe even though this MVP cannot yet prove the error is new.
	diagnosticPaths := map[string]bool{}
	for path := range changedPaths {
		diagnosticPaths[path] = true
	}
	for path := range declarationAffected {
		if !changedPaths[path] {
			diagnosticPaths[path] = true
		}
	}
	paths := sortedBoolKeys(diagnosticPaths)
	if len(paths) > verifyMaxDiagnosticFiles {
		warnings = append(warnings, "diagnostic verification capped at 32 files")
		paths = paths[:verifyMaxDiagnosticFiles]
		complete = false
	}
	diagnostics := make([]core.Diagnostic, 0)
	for _, path := range paths {
		p, profileErr := language.FromPath(path)
		if profileErr != nil || !e.Sessions.Configured(p.SessionKey) {
			continue
		}
		result, diagErr := e.Diagnostics(ctx, map[string]any{"path": path, "severities": []any{"error"}, "limit": limit})
		if diagErr != nil {
			warnings = append(warnings, "diagnostics unavailable for "+path+": "+diagErr.Error())
			complete = false
			continue
		}
		ds, _ := result["diagnostics"].([]core.Diagnostic)
		for _, d := range ds {
			diagnostics = append(diagnostics, d)
			if changedPaths[path] && lineInChangedRanges(d.Range.Start.Line, changedRanges[path]) {
				findings = append(findings, verifyFinding{Severity: "error", Code: "LSP_ERROR_ON_CHANGED_LINE", Path: path, Message: d.Message})
			} else if declarationAffected[path] {
				findings = append(findings, verifyFinding{Severity: "error", Code: "LSP_ERROR_IN_AFFECTED_FILE", Path: path, Message: d.Message})
			}
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		if severityRank(findings[i].Severity) != severityRank(findings[j].Severity) {
			return severityRank(findings[i].Severity) > severityRank(findings[j].Severity)
		}
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Code < findings[j].Code
	})
	verdict := "PASS"
	for _, finding := range findings {
		if finding.Severity == "error" {
			verdict = "FAIL"
			break
		}
		if finding.Severity == "warning" {
			verdict = "WARNING"
		}
	}

	symbolOutput := make([]any, 0, len(changedSymbols))
	for _, changed := range changedSymbols {
		symbolOutput = append(symbolOutput, map[string]any{
			"symbol_id": changed.SymbolID, "symbol_path": changed.Node.SymbolPath,
			"name": changed.Node.Name, "kind": changed.Node.Kind, "language": changed.Profile.Name,
			"path": changed.Path, "range": changed.Node.Range, "change_kind": changed.ChangeKind,
		})
	}
	return map[string]any{
		"verdict": verdict, "base": changes.Base,
		"changed_files": fileOutput, "changed_symbols": symbolOutput,
		"affected_files": affectedOutput, "related_tests": relatedTests,
		"diagnostics": diagnostics, "findings": findings,
		"meta": core.Meta{Complete: complete, Warnings: warnings},
	}, nil
}

func (e *Engine) verifyAffectedFiles(ctx context.Context, changed changedSemanticSymbol, depth, limit int) (map[string][]string, []string) {
	if callableSymbolKind(changed.Node.Kind) {
		in := map[string]any{"symbol_id": changed.SymbolID, "depth": depth, "limit": limit}
		impact, err := e.ImpactAnalysis(ctx, in)
		if err == nil {
			return affectedFromImpact(impact), metaWarnings(impact)
		}
		warnings := []string{"impact analysis unavailable for " + changed.Path + "#" + changed.Node.SymbolPath + ": " + err.Error()}
		fallback, fallbackWarnings := e.verifyContextAffectedFiles(ctx, changed, limit, []any{"incoming_calls", "references"})
		return fallback, append(warnings, fallbackWarnings...)
	}

	include := []any{"references"}
	if typeLikeSymbolKind(changed.Node.Kind) {
		include = append(include, "implementations")
	}
	return e.verifyContextAffectedFiles(ctx, changed, limit, include)
}

func (e *Engine) verifyContextAffectedFiles(ctx context.Context, changed changedSemanticSymbol, limit int, include []any) (map[string][]string, []string) {
	contextResult, err := e.SymbolContext(ctx, map[string]any{
		"symbol_id": changed.SymbolID, "include": include, "limit": limit,
	})
	if err != nil {
		return nil, []string{"semantic context unavailable for " + changed.Path + "#" + changed.Node.SymbolPath + ": " + err.Error()}
	}
	return affectedFromContext(contextResult), metaWarnings(contextResult)
}

func callableSymbolKind(kind string) bool {
	switch kind {
	case "function", "method", "constructor":
		return true
	default:
		return false
	}
}

func typeLikeSymbolKind(kind string) bool {
	switch kind {
	case "class", "interface", "struct", "enum":
		return true
	default:
		return false
	}
}

func affectedFromImpact(result map[string]any) map[string][]string {
	out := map[string][]string{}
	entries, _ := result["affected_files"].([]any)
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		path, _ := entry["path"].(string)
		if path == "" {
			continue
		}
		for _, reason := range stringsFromAny(entry["reasons"]) {
			out[path] = mergeStrings(out[path], reason)
		}
	}
	return out
}

func affectedFromContext(result map[string]any) map[string][]string {
	out := map[string][]string{}
	calls, _ := result["incoming_calls"].([]any)
	for _, raw := range calls {
		entry, _ := raw.(map[string]any)
		if path, _ := entry["path"].(string); path != "" {
			out[path] = mergeStrings(out[path], "caller")
		}
	}
	refs, _ := result["references"].([]any)
	for _, raw := range refs {
		entry, _ := raw.(map[string]any)
		if path, _ := entry["path"].(string); path != "" {
			out[path] = mergeStrings(out[path], "reference")
		}
	}
	impls, _ := result["implementations"].([]core.Location)
	for _, loc := range impls {
		out[loc.Path] = mergeStrings(out[loc.Path], "implementation")
	}
	return out
}

func nodesForChangedRanges(nodes []symbolNode, ranges []changedLineRange) []symbolNode {
	seen := map[string]bool{}
	var out []symbolNode
	for _, r := range ranges {
		for _, node := range deepestOverlappingNodes(nodes, r) {
			key := node.SymbolPath + "#" + node.Kind
			if !seen[key] {
				seen[key] = true
				out = append(out, node)
			}
		}
	}
	return out
}

func deepestOverlappingNodes(nodes []symbolNode, r changedLineRange) []symbolNode {
	var out []symbolNode
	for _, node := range nodes {
		if !lineRangesOverlap(node.Range.Start.Line, node.Range.End.Line, r.Start, r.End) {
			continue
		}
		children := deepestOverlappingNodes(node.Children, r)
		if len(children) > 0 {
			out = append(out, children...)
		} else {
			out = append(out, node)
		}
	}
	return out
}

func nodeDeclarationChanged(node symbolNode, ranges []changedLineRange) bool {
	start := node.Range.Start.Line
	end := node.SelectionRange.End.Line
	if end < start {
		end = start
	}
	for _, r := range ranges {
		if lineRangesOverlap(start, end, r.Start, r.End) {
			return true
		}
	}
	return false
}

func lineRangesOverlap(aStart, aEnd, bStart, bEnd int) bool {
	return aStart <= bEnd && bStart <= aEnd
}

func lineInChangedRanges(line int, ranges []changedLineRange) bool {
	for _, r := range ranges {
		if line >= r.Start && line <= r.End {
			return true
		}
	}
	return false
}

func isTestSourcePath(path string) bool {
	p := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(p)
	return strings.Contains(p, "/test/") || strings.Contains(p, "/tests/") ||
		strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.go") ||
		strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}

func mergeStrings(values []string, more ...string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range more {
		if value != "" && !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}
	sort.Strings(values)
	return values
}

func stringsFromAny(v any) []string {
	switch xs := v.(type) {
	case []string:
		return xs
	case []any:
		out := make([]string, 0, len(xs))
		for _, x := range xs {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func sortedBoolKeys(values map[string]bool) []string { return sortedKeys(values) }

func severityRank(severity string) int {
	switch severity {
	case "error":
		return 3
	case "warning":
		return 2
	default:
		return 1
	}
}

func metaWarnings(result map[string]any) []string {
	if meta, ok := result["meta"].(core.Meta); ok {
		return append([]string(nil), meta.Warnings...)
	}
	return nil
}
