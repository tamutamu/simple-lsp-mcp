package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

// PreviewRename is deliberately a separate read-only entry point. Some
// noninteractive MCP clients refuse tools annotated as destructive even when
// the caller passes apply=false. Keep the existing RenameSymbol API for
// compatibility, but never allow a preview invocation to write.
func (e *Engine) PreviewRename(ctx context.Context, in map[string]any) (map[string]any, error) {
	detail := stringVal(in, "preview_detail")
	if detail != "" && detail != "summary" && detail != "full" {
		return nil, core.NewError(core.InvalidArgument, "preview_detail must be summary or full")
	}
	preview := make(map[string]any, len(in)+1)
	for key, value := range in {
		preview[key] = value
	}
	preview["apply"] = false
	result, err := e.RenameSymbol(ctx, preview)
	if err != nil {
		return nil, err
	}
	if detail == "full" {
		return result, nil
	}
	return compactRenamePreview(result), nil
}

// A project-wide semantic rename may update hundreds of locations. Repeating
// each replacement string and complete range in the MCP response is usually
// wasted model context when the agent first wants to know which files change.
// All edits have already passed the exact workspaceEditPlan validation.
// Keep the complete preview available via preview_detail=full for deliberate auditing.
// This only compacts preview_rename; rename_symbol remains backward compatible.
func compactRenamePreview(result map[string]any) map[string]any {
	files, ok := result["files"].([]fileEditPreview)
	if !ok {
		return result
	}
	const inlineEditLimit = 12
	const samplePerFile = 2
	const maxTotalSamples = 12
	totalEdits := 0
	for _, file := range files {
		totalEdits += len(file.Edits)
	}
	if totalEdits <= inlineEditLimit {
		return result
	}
	summaries := make([]map[string]any, 0, len(files))
	sampled := 0
	for _, file := range files {
		entry := map[string]any{
			"path":       file.Path,
			"edit_count": len(file.Edits),
		}
		if len(file.Edits) > 0 {
			entry["first_line"] = file.Edits[0].Range.Start.Line
			entry["last_line"] = file.Edits[len(file.Edits)-1].Range.Start.Line
		}
		budget := samplePerFile
		if remaining := maxTotalSamples - sampled; budget > remaining {
			budget = remaining
		}
		if budget > len(file.Edits) {
			budget = len(file.Edits)
		}
		if budget > 0 {
			entry["sample_edits"] = file.Edits[:budget]
			sampled += budget
		}
		if omitted := len(file.Edits) - budget; omitted > 0 {
			entry["omitted_edits"] = omitted
		}
		summaries = append(summaries, entry)
	}
	result["files"] = summaries
	result["preview_detail"] = "summary"
	result["preview_truncated"] = true
	result["sample_edit_count"] = sampled
	result["full_preview_available"] = "preview_rename(preview_detail=full)"
	return result
}

// CompactAppliedRename retains the result needed after a verified write,
// without echoing every replacement already applied to the filesystem.
// The full plan remains available through RenameSymbol and preview_rename.
func CompactAppliedRename(result map[string]any) map[string]any {
	if applied, _ := result["applied"].(bool); !applied {
		return result
	}
	files, ok := result["files"].([]fileEditPreview)
	if !ok {
		return result
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	summary := map[string]any{
		"applied":       true,
		"new_name":      result["new_name"],
		"file_count":    result["file_count"],
		"edit_count":    result["edit_count"],
		"changed_files": paths,
	}
	if warnings, present := result["warnings"]; present {
		summary["warnings"] = warnings
	}
	if count, present := result["targets_count"]; present {
		summary["targets_count"] = count
	}
	if counts, present := result["target_edit_counts"]; present {
		summary["target_edit_counts"] = counts
	}
	for _, key := range []string{"unverified_build_tag_files", "build_tag_candidates_truncated", "coverage_complete", "unrenamed_method_candidates", "method_discovery_truncated", "method_discovery_parse_errors", "fluctuating_rename_targets", "rename_plan_fluctuated", "covered_target_indices"} {
		if value, present := result[key]; present {
			summary[key] = value
		}
	}
	return summary
}

// RenameSymbol asks the selected language server to compute a semantic rename.
// It is preview-only unless apply=true. The server never computes replacements
// itself; it only validates and optionally applies the returned WorkspaceEdit.
type computedRename struct {
	edit             protocol.WorkspaceEdit
	encoding         string
	sessionKey       string
	oldName          string
	goPlanFluctuated bool
	preparation      map[string]any
}

func (e *Engine) RenameSymbol(ctx context.Context, in map[string]any) (map[string]any, error) {
	if raw, hasTargets := in["targets"]; hasTargets {
		return e.renameMultipleSymbols(ctx, in, raw)
	}
	computed, err := e.computeRenameEdit(ctx, in)
	if err != nil {
		return nil, err
	}
	plan, files, count, err := e.workspaceEditPlan(computed.edit, computed.encoding)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, core.NewError(core.IncompleteSearch, "language server returned no semantic rename edits")
	}
	var tagScan renameTagScan
	if computed.sessionKey == "go" {
		tagScan, err = e.taggedGoRenameCandidates(ctx, []string{computed.oldName}, files)
		if err != nil {
			return nil, core.WithCause(core.IncompleteSearch, "could not inspect alternate Go build-tag source before rename", err)
		}
	}
	var siblingScan renameSiblingScan
	if computed.sessionKey == "go" {
		siblingScan, err = e.findUnrenamedGoMethods(ctx, computed.oldName, files)
		if err != nil {
			return nil, core.WithCause(core.IncompleteSearch, "could not inspect independent method declarations before rename", err)
		}
	}
	result := map[string]any{"new_name": stringVal(in, "new_name"), "applied": false,
		"file_count": len(files), "edit_count": count, "files": files}
	if computed.preparation != nil {
		result["prepare"] = computed.preparation
	}
	attachBuildTagRenameWarning(result, tagScan)
	attachSiblingRenameWarning(result, siblingScan)
	if computed.goPlanFluctuated {
		appendRenamePlanWarning(result)
	}
	if !boolValDefault(in, "apply", false) {
		return result, nil
	}
	warnings, err := e.applyWorkspaceEditPlan(ctx, plan)
	if err != nil {
		return nil, err
	}
	result["applied"] = true
	if len(warnings) > 0 {
		prior, _ := result["warnings"].([]string)
		result["warnings"] = append(prior, warnings...)
	}
	return result, nil
}

// computeRenameEdit requests and validates one semantic rename from its LSP,
// without modifying the filesystem. Keeping it independent of applying edits
// lets renameMultipleSymbols validate an entire cross-interface change first.
func (e *Engine) computeRenameEdit(ctx context.Context, in map[string]any) (computedRename, error) {
	name := stringVal(in, "new_name")
	if strings.TrimSpace(name) == "" {
		return computedRename{}, core.NewError(core.InvalidArgument, "new_name must be a non-empty string")
	}
	p, s, d, pos, err := e.target(ctx, in)
	if err != nil {
		return computedRename{}, err
	}
	caps := s.Capabilities()
	if !caps.Rename {
		return computedRename{}, e.unsupported(p, "textDocument/rename")
	}
	var preparation map[string]any
	if caps.PrepareRename {
		callCtx, cancel := e.callContext(ctx)
		var raw json.RawMessage
		err = s.Request(callCtx, "textDocument/prepareRename",
			map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}, "position": pos}, &raw)
		cancel()
		if err != nil {
			return computedRename{}, err
		}
		preparation, err = renamePreparation(d.Text, raw, caps.PositionEncoding)
		if err != nil {
			return computedRename{}, err
		}
	}
	if p.SessionKey == "typescript-javascript" || p.SessionKey == "python" {
		// Open only candidate sources, while the language server is still the
		// sole authority for renaming. Its edit plan may be incomplete until
		// the opened-project source is primed.
		identifier := e.renameWarmCandidateName(in, d.Text, preparation, pos, caps.PositionEncoding)
		if identifier != "" {
			if err = e.warmProjectSymbolCandidates(ctx, p, s, identifier); err != nil {
				return computedRename{}, err
			}
			if err = e.primeProjectSemantics(ctx, p, s, d, pos); err != nil {
				return computedRename{}, err
			}
		}
	}
	if p.SessionKey == "go" && caps.References {
		// gopls may return a partial rename during its initial background
		// package loading. Resolve actual semantic references first: this
		// loads the relevant package graph before requesting edits. On
		// Teleport this changes a cold 41/44-file result to the complete
		// 45-file LSP edit plan without arbitrary sleeps.
		callCtx, cancel := e.callContext(ctx)
		var refs json.RawMessage
		err := s.Request(callCtx, "textDocument/references", map[string]any{
			"textDocument": protocol.TextDocumentIdentifier{URI: d.URI},
			"position":     pos,
			"context":      map[string]any{"includeDeclaration": true},
		}, &refs)
		cancel()
		if err != nil {
			return computedRename{}, core.WithCause(core.IncompleteSearch,
				"could not initialize gopls semantic references before rename; no files were changed", err)
		}
	}
	callCtx, cancel := e.callContext(ctx)
	var edit protocol.WorkspaceEdit
	err = s.Request(callCtx, "textDocument/rename", map[string]any{
		"textDocument": protocol.TextDocumentIdentifier{URI: d.URI}, "position": pos, "newName": name,
	}, &edit)
	cancel()
	if err != nil {
		return computedRename{}, classifySemanticRenameError(p.SessionKey, err)
	}
	var fluctuated bool
	if p.SessionKey == "go" {
		// A gopls response can alternate between a 44-file subset and a
		// 45-file superset even after reference priming. We never merge
		// independently produced edits. Instead, compare normalized and
		// validated plans and select an *actual* LSP response that contains
		// every edit from every observed snapshot.
		_, firstFiles, firstCount, planErr := e.workspaceEditPlan(edit, caps.PositionEncoding)
		if planErr != nil {
			return computedRename{}, planErr
		}
		snapshots := []renameEditSnapshot{{Edit: edit, Files: firstFiles, Count: firstCount}}
		stable := 0
		for attempt := 0; attempt < 6; attempt++ {
			callCtx, cancel := e.callContext(ctx)
			var next protocol.WorkspaceEdit
			err := s.Request(callCtx, "textDocument/rename", map[string]any{
				"textDocument": protocol.TextDocumentIdentifier{URI: d.URI},
				"position":     pos, "newName": name,
			}, &next)
			cancel()
			if err != nil {
				return computedRename{}, classifySemanticRenameError(p.SessionKey, err)
			}
			_, normalized, editCount, planErr := e.workspaceEditPlan(next, caps.PositionEncoding)
			if planErr != nil {
				return computedRename{}, planErr
			}
			previous := snapshots[len(snapshots)-1]
			if reflect.DeepEqual(previous.Files, normalized) {
				stable++
			} else {
				stable = 0
			}
			snapshots = append(snapshots, renameEditSnapshot{
				Edit: next, Files: normalized, Count: editCount,
			})
			if stable >= 2 {
				break
			}
		}
		chosen, changed, selectErr := dominatingRenameSnapshot(snapshots)
		if selectErr != nil {
			return computedRename{}, core.WithCause(core.IncompleteSearch,
				"gopls rename plans disagreed without a safe validated superset; no files were changed", selectErr)
		}
		edit = chosen.Edit
		fluctuated = changed
	}
	return computedRename{edit: edit, encoding: caps.PositionEncoding,
		sessionKey: p.SessionKey, oldName: warmIdentifierAtPosition(d.Text, pos, caps.PositionEncoding),
		preparation: preparation, goPlanFluctuated: fluctuated}, nil
}

// renameEditSnapshot contains one validated response from the language
// server. Choosing an observed superset is safer than synthesizing a union of
// edits that no individual semantic request ever returned.
type renameEditSnapshot struct {
	Edit  protocol.WorkspaceEdit
	Files []fileEditPreview
	Count int
}

// dominatingRenameSnapshot chooses an actual LSP edit response which contains
// every edit seen in all snapshots. Equal plans with different order are
// equivalent after workspaceEditPlan normalization. Conflicting or disjoint
// plans fail closed rather than silently applying a partial rename.
func dominatingRenameSnapshot(snapshots []renameEditSnapshot) (renameEditSnapshot, bool, error) {
	if len(snapshots) == 0 {
		return renameEditSnapshot{}, false, fmt.Errorf("no semantic rename snapshots")
	}
	best := 0
	for i := 1; i < len(snapshots); i++ {
		if snapshots[i].Count > snapshots[best].Count {
			best = i
		}
	}
	fluctuated := false
	for i, snapshot := range snapshots {
		if !renameEditsSubset(snapshot.Files, snapshots[best].Files) {
			return renameEditSnapshot{}, false, fmt.Errorf(
				"snapshot %d (%d edits) is not a subset of largest response (%d edits)",
				i, snapshot.Count, snapshots[best].Count)
		}
		if !reflect.DeepEqual(snapshot.Files, snapshots[best].Files) {
			fluctuated = true
		}
	}
	return snapshots[best], fluctuated, nil
}

func renameEditsSubset(smaller, larger []fileEditPreview) bool {
	index := make(map[string]map[editPreview]bool, len(larger))
	for _, file := range larger {
		byEdit := make(map[editPreview]bool, len(file.Edits))
		for _, edit := range file.Edits {
			byEdit[edit] = true
		}
		index[file.Path] = byEdit
	}
	for _, file := range smaller {
		byEdit, ok := index[file.Path]
		if !ok {
			return false
		}
		for _, edit := range file.Edits {
			if !byEdit[edit] {
				return false
			}
		}
	}
	return true
}

// renameMultipleSymbols gathers independently computed LSP WorkspaceEdits on
// the same unchanged source, validates the union (including overlapping edits)
// and writes all files atomically with the existing rollback machinery.
// Only explicit, independently identified symbols are included: no lexical
// match is silently treated as equivalent to another interface method.
func (e *Engine) renameMultipleSymbols(ctx context.Context, in map[string]any, raw any) (map[string]any, error) {
	name := stringVal(in, "new_name")
	if strings.TrimSpace(name) == "" {
		return nil, core.NewError(core.InvalidArgument, "new_name must be a non-empty string")
	}
	items, ok := raw.([]any)
	if !ok || len(items) < 2 || len(items) > 6 {
		return nil, core.NewError(core.InvalidArgument, "targets must contain 2 to 6 symbol identities")
	}
	for _, field := range []string{"path", "symbol_path", "symbol_id", "line", "column", "language"} {
		if _, present := in[field]; present {
			return nil, core.NewError(core.InvalidArgument, "targets cannot be combined with top-level "+field)
		}
	}
	validated := make([]map[string]any, 0, len(items))
	seen := map[string]bool{}
	for i, item := range items {
		target, ok := item.(map[string]any)
		if !ok {
			return nil, core.NewError(core.InvalidArgument, fmt.Sprintf("targets[%d] must be an object", i))
		}
		for key := range target {
			switch key {
			case "path", "symbol_path", "symbol_id", "line", "column", "language":
			default:
				return nil, core.NewError(core.InvalidArgument, fmt.Sprintf("unknown field %s in targets[%d]", key, i))
			}
		}
		if err := targetOf(target).Validate(); err != nil {
			return nil, core.WithCause(core.InvalidArgument, fmt.Sprintf("invalid targets[%d]", i), err)
		}
		identity, _ := json.Marshal(target)
		if seen[string(identity)] {
			return nil, core.NewError(core.InvalidArgument, fmt.Sprintf("duplicate targets[%d]", i))
		}
		seen[string(identity)] = true
		part := make(map[string]any, len(target)+1)
		for k, v := range target {
			part[k] = v
		}
		part["new_name"] = name
		validated = append(validated, part)
	}
	// Resolve and open ALL requested target documents before issuing any rename
	// request. Go type-checking/index state can otherwise depend on which
	// interface document was first opened, producing a partial edit set.
	type resolvedBatchTarget struct {
		URI      string
		Position protocol.Position
		OldName  string
	}
	resolved := make([]resolvedBatchTarget, 0, len(validated))
	for i, target := range validated {
		_, session, doc, pos, err := e.target(ctx, target)
		if err != nil {
			return nil, core.WithCause(core.IncompleteSearch,
				fmt.Sprintf("targets[%d] could not be resolved before rename; no files changed", i), err)
		}
		oldName := warmIdentifierAtPosition(doc.Text, pos, session.Capabilities().PositionEncoding)
		if oldName == "" {
			return nil, core.NewError(core.InvalidArgument,
				fmt.Sprintf("targets[%d] did not resolve to a renameable identifier", i))
		}
		resolved = append(resolved, resolvedBatchTarget{
			URI: doc.URI, Position: pos, OldName: oldName,
		})
	}
	combined := protocol.WorkspaceEdit{Changes: map[string][]protocol.TextEdit{}}
	targetEdits := make([]int, 0, len(items))
	targetFiles := make([]int, 0, len(items))
	oldNames := make([]string, 0, len(items))
	var renamePlanVariations int
	alreadyCovered := make([]int, 0)
	var encoding, sessionKey string
	for i, target := range validated {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if i > 0 && resolved[i].OldName != oldNames[0] {
			return nil, core.NewError(core.InvalidArgument,
				"batch rename requires the same original method name for every target; no files changed")
		}
		if i > 0 && renameEditCoversTarget(combined, resolved[i].URI, resolved[i].Position) {
			// An implementation method can already be included in the
			// preceding interface's semantic edit. Avoid the redundant
			// gopls request that would reject it as breaking that interface.
			alreadyCovered = append(alreadyCovered, i)
			targetEdits = append(targetEdits, 0)
			targetFiles = append(targetFiles, 0)
			oldNames = append(oldNames, resolved[i].OldName)
			continue
		}
		part, err := e.computeRenameEdit(ctx, target)
		if err != nil {
			return nil, core.WithCause(core.IncompleteSearch, fmt.Sprintf("targets[%d] rename failed without writing files", i), err)
		}
		if i == 0 {
			encoding, sessionKey = part.encoding, part.sessionKey
		} else if encoding != part.encoding || sessionKey != part.sessionKey {
			return nil, core.NewError(core.InvalidArgument, "batch rename requires one language-server profile and position encoding")
		}
		// Every individual request must produce a valid, non-empty edit plan.
		// Never report success for targets silently omitted by a language server.
		_, partFiles, partCount, err := e.workspaceEditPlan(part.edit, part.encoding)
		if err != nil {
			return nil, core.WithCause(core.IncompleteSearch,
				fmt.Sprintf("targets[%d] returned invalid edits; no files changed", i), err)
		}
		if partCount == 0 {
			return nil, core.NewError(core.IncompleteSearch,
				fmt.Sprintf("targets[%d] returned no semantic edits; no files changed", i))
		}
		if part.oldName == "" || (i > 0 && part.oldName != oldNames[0]) {
			return nil, core.NewError(core.InvalidArgument,
				"batch rename requires all targets to have the same existing method name; no files changed")
		}
		targetEdits = append(targetEdits, partCount)
		targetFiles = append(targetFiles, len(partFiles))
		oldNames = append(oldNames, part.oldName)
		if part.goPlanFluctuated {
			renamePlanVariations++
		}
		for uri, edits := range part.edit.Changes {
			combined.Changes[uri] = append(combined.Changes[uri], edits...)
		}
		combined.DocumentChanges = append(combined.DocumentChanges, part.edit.DocumentChanges...)
	}
	plan, files, count, err := e.workspaceEditPlan(combined, encoding)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, core.NewError(core.IncompleteSearch, "all targets returned zero rename edits")
	}
	var tagScan renameTagScan
	if sessionKey == "go" {
		tagScan, err = e.taggedGoRenameCandidates(ctx, oldNames, files)
		if err != nil {
			return nil, core.WithCause(core.IncompleteSearch, "could not inspect alternate Go build-tag source before batch rename", err)
		}
	}
	var siblingScan renameSiblingScan
	if sessionKey == "go" && len(oldNames) > 0 {
		siblingScan, err = e.findUnrenamedGoMethods(ctx, oldNames[0], files)
		if err != nil {
			return nil, core.WithCause(core.IncompleteSearch, "could not inspect independent method declarations before batch rename", err)
		}
	}
	result := map[string]any{"new_name": name, "targets_count": len(items), "applied": false,
		"file_count": len(files), "edit_count": count, "files": files,
		"target_edit_counts": targetEdits, "target_file_counts": targetFiles}
	if len(alreadyCovered) > 0 {
		result["covered_target_indices"] = alreadyCovered
	}
	attachBuildTagRenameWarning(result, tagScan)
	attachSiblingRenameWarning(result, siblingScan)
	if renamePlanVariations > 0 {
		result["fluctuating_rename_targets"] = renamePlanVariations
		appendRenamePlanWarning(result)
	}
	if !boolValDefault(in, "apply", false) {
		return result, nil
	}
	warnings, err := e.applyWorkspaceEditPlan(ctx, plan)
	if err != nil {
		return nil, err
	}
	result["applied"] = true
	if len(warnings) > 0 {
		prior, _ := result["warnings"].([]string)
		result["warnings"] = append(prior, warnings...)
	}
	return result, nil
}

// renameEditCoversTarget checks whether a previously generated LSP edit
// already contains an exact declaration position. This is proof that the
// selected method will be renamed; it is not a guess based on a shared name.
func renameEditCoversTarget(edit protocol.WorkspaceEdit, uri string, point protocol.Position) bool {
	contains := func(edits []protocol.TextEdit) bool {
		for _, change := range edits {
			if change.Range.Start == point {
				return true
			}
		}
		return false
	}
	if contains(edit.Changes[uri]) {
		return true
	}
	for _, raw := range edit.DocumentChanges {
		var document protocol.TextDocumentEdit
		if err := json.Unmarshal(raw, &document); err == nil &&
			document.TextDocument.URI == uri && contains(document.Edits) {
			return true
		}
	}
	return false
}

// A varying response was accepted only because one exact LSP WorkspaceEdit
// safely contained every other observed edit. Surface this limitation to the
// agent for follow-up validation rather than claiming comprehensive indexing.
func appendRenamePlanWarning(result map[string]any) {
	result["rename_plan_fluctuated"] = true
	prior, _ := result["warnings"].([]string)
	result["warnings"] = append(prior,
		"gopls rename snapshots varied; applied the validated LSP response containing every observed edit. Verify affected files and alternate build configurations.")
}

// attachSiblingRenameWarning reports other declarations with the same method
// spelling that are not part of this LSP edit plan. They may be separate Go
// interfaces; no semantic equivalence is assumed and no automatic edits occur.
func attachSiblingRenameWarning(result map[string]any, scan renameSiblingScan) {
	if len(scan.Candidates) == 0 && !scan.Truncated && scan.ParseErrors == 0 {
		return
	}
	if len(scan.Candidates) > 0 {
		result["unrenamed_method_candidates"] = scan.Candidates
	}
	if scan.Truncated {
		result["method_discovery_truncated"] = true
	}
	if scan.ParseErrors > 0 {
		result["method_discovery_parse_errors"] = scan.ParseErrors
	}
	prior, _ := result["warnings"].([]string)
	result["warnings"] = append(prior,
		"Other Go declarations with the old method name were not in this LSP rename edit; review unrenamed_method_candidates before declaring the multi-interface refactor complete. These are syntax-level candidates, not proven dependencies.")
}

// attachBuildTagRenameWarning reports potentially stale method usages that
// the active gopls build configuration did not include. The files are lexical
// candidates only: they MUST be reviewed semantically before any further edit.
func attachBuildTagRenameWarning(result map[string]any, scan renameTagScan) {
	if len(scan.Paths) == 0 && !scan.Truncated {
		return
	}
	result["unverified_build_tag_files"] = scan.Paths
	result["coverage_complete"] = false
	if scan.Truncated {
		result["build_tag_candidates_truncated"] = true
	}
	result["warnings"] = append([]string{},
		"Go build-tag variants may contain old identifier usages not covered by this LSP WorkspaceEdit; review unverified_build_tag_files before claiming a complete rename")
}

// classifySemanticRenameError preserves an exact gopls constraint instead of
// reporting it as a generic LSP server failure. A concrete method must
// continue to satisfy its Go interface; retrying with a regex/text
// replacement would break callers. Guide the agent to the interface
// declaration for a safe, full-program semantic rename.
func classifySemanticRenameError(sessionKey string, err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if sessionKey == "go" &&
		strings.Contains(message, "renaming this method") &&
		strings.Contains(message, "no longer assignable to interface") {
		return core.WithCause(core.InvalidArgument,
			"Go rename would break an interface implementation; rename the corresponding interface method instead so gopls can update implementations and callers together",
			err)
	}
	return err
}

func (e *Engine) renameWarmCandidateName(in map[string]any, text []byte, preparation map[string]any, position protocol.Position, encoding string) string {
	name := e.renameWarmName(in, text, preparation)
	if name != "" {
		return name
	}
	return warmIdentifierAtPosition(text, position, encoding)
}

func (e *Engine) renameWarmName(in map[string]any, text []byte, preparation map[string]any) string {
	if preparation != nil {
		if placeholder, _ := preparation["placeholder"].(string); placeholder != "" {
			return placeholder
		}
		if r, ok := preparation["range"].(core.Range); ok {
			start, startErr := byteOffsetAtCorePosition(text, r.Start)
			end, endErr := byteOffsetAtCorePosition(text, r.End)
			if startErr == nil && endErr == nil && start < end && end <= len(text) {
				return string(text[start:end])
			}
		}
	}
	return e.targetWarmName(in)
}

func renamePreparation(text []byte, raw json.RawMessage, encoding string) (map[string]any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, core.NewError(core.InvalidArgument, "language server does not allow rename at the target")
	}
	var direct protocol.Range
	if json.Unmarshal(trimmed, &direct) == nil {
		var probe map[string]json.RawMessage
		if json.Unmarshal(trimmed, &probe) == nil && probe["start"] != nil && probe["end"] != nil {
			converted, err := rangeFromText(text, direct, encoding)
			if err != nil {
				return nil, err
			}
			return map[string]any{"range": converted}, nil
		}
	}
	var options struct {
		Range           *protocol.Range `json:"range,omitempty"`
		Placeholder     string          `json:"placeholder,omitempty"`
		DefaultBehavior bool            `json:"defaultBehavior,omitempty"`
	}
	if err := json.Unmarshal(trimmed, &options); err != nil {
		return nil, core.WithCause(core.InternalError, "invalid prepareRename response", err)
	}
	out := map[string]any{}
	if options.Range != nil {
		converted, err := rangeFromText(text, *options.Range, encoding)
		if err != nil {
			return nil, err
		}
		out["range"] = converted
	}
	if options.Placeholder != "" {
		out["placeholder"] = options.Placeholder
	}
	if options.DefaultBehavior {
		out["default_behavior"] = true
	}
	if len(out) == 0 {
		return nil, core.NewError(core.InvalidArgument, "language server returned an unusable prepareRename response")
	}
	return out, nil
}
