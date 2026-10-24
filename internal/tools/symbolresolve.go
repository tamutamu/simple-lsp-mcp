package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/document"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/session"
	"github.com/tamutamu/simple-lsp-mcp/internal/normalize"
)

// maxProbeFiles prevents unbounded LSP fan-out. Searches exceeding it fail
// explicitly rather than returning a false unique match or false not-found.
const maxProbeFiles = 128

// symbolLocation is one resolved symbol_path hit, holding everything needed
// to target it exactly like a symbol_id or a path+line+column would be.
type symbolLocation struct {
	Profile  language.Profile
	Session  *session.Session
	Document document.Document
	Node     symbolNode
}

// documentNodes fetches and walks the document symbol tree of one file. A
// flat SymbolInformation[] response (no parent/child structure) becomes a
// set of single-segment root-level nodes; matchNodes can still find them by
// name, just without any nesting.
func (e *Engine) documentNodes(ctx context.Context, p language.Profile, path string) (*session.Session, document.Document, []symbolNode, error) {
	_, s, d, err := e.document(ctx, path, p.Name)
	if err != nil {
		return nil, document.Document{}, nil, err
	}
	raw, err := e.requestDocumentSymbolTree(ctx, p, s, d)
	if err != nil {
		return nil, document.Document{}, nil, err
	}
	var tree []protocol.DocumentSymbol
	if json.Unmarshal(raw, &tree) == nil && tree != nil {
		return s, d, walkDocument(d.Text, s.Capabilities().PositionEncoding, tree, nil, ""), nil
	}
	var flat []protocol.SymbolInformation
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, document.Document{}, nil, err
	}
	nodes := make([]symbolNode, 0, len(flat))
	for _, v := range flat {
		rr, err := rangeFromText(d.Text, v.Location.Range, s.Capabilities().PositionEncoding)
		if err != nil {
			continue
		}
		segments := []string{v.Name}
		nodes = append(nodes, symbolNode{
			Name: v.Name, Kind: normalize.Kind(v.Kind),
			Segments: segments, SymbolPath: joinSymbolPath(segments),
			ContainerName: v.ContainerName, Range: rr, SelectionRange: rr,
		})
	}
	return s, d, nodes, nil
}

// candidateFiles asks the language server's workspace symbol index which
// files might contain a symbol path's leaf name. Results are not
// registered in the symbol registry: most of them will never be used, and
// registering unused candidates would only grow the registry for nothing.
func (e *Engine) candidateFiles(ctx context.Context, p language.Profile, leaf string) ([]string, error) {
	servers, err := e.Sessions.ForAll(p.SessionKey)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	for _, s := range servers {
		var raw []protocol.WorkspaceSymbol
		callCtx, cancel := e.callContext(ctx)
		err := s.Request(callCtx, "workspace/symbol", map[string]string{"query": leaf}, &raw)
		cancel()
		if err == nil && len(raw) > 0 {
			for _, v := range raw {
				path, err := normalize.URIPath(e.WS, v.Location.URI)
				if err != nil || seen[path] {
					continue
				}
				seen[path] = true
				files = append(files, path)
			}
			continue
		}

		// Some servers (notably tsserver and Pyright) may not populate
		// workspace/symbol until at least one project document has been opened.
		// Fall back to a bounded source-file traversal under this exact LSP root;
		// documentSymbol then provides authoritative symbol structure without a
		// hidden index. If the root is too large, fail closed and ask for path.
		fallback, fallbackErr := e.sessionCandidateFiles(ctx, p, s)
		if fallbackErr != nil {
			if err != nil {
				return nil, core.WithCause(core.IncompleteSearch, p.SessionKey+" workspace-symbol query failed and fallback discovery could not complete; specify path to narrow the search", err)
			}
			return nil, fallbackErr
		}
		for _, path := range fallback {
			if seen[path] {
				continue
			}
			seen[path] = true
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files, nil
}

func (e *Engine) sessionCandidateFiles(ctx context.Context, p language.Profile, s *session.Session) ([]string, error) {
	root, err := filepath.EvalSymlinks(filepath.Clean(s.Root()))
	if err != nil {
		return nil, core.WithCause(core.IncompleteSearch, "configured language-server root escapes the workspace; provide an in-workspace path/profile", err)
	}
	if _, err := e.WS.Relative(root); err != nil {
		return nil, core.WithCause(core.IncompleteSearch, "configured language-server root escapes the workspace; provide an in-workspace path/profile", err)
	}
	var files []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && workspaceListExcludedDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(e.WS.Root(), path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		profile, err := language.FromPath(rel)
		if err != nil || profile.SessionKey != p.SessionKey {
			return nil
		}
		files = append(files, rel)
		if len(files) > maxProbeFiles {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("more than %d source files under %s require fallback discovery; provide path to narrow the query", maxProbeFiles, filepath.ToSlash(root)))
		}
		return nil
	})
	if err != nil {
		var appErr *core.AppError
		if errors.As(err, &appErr) {
			return nil, err
		}
		return nil, core.WithCause(core.IncompleteSearch, "could not enumerate fallback source files; provide path to narrow the query", err)
	}
	return files, nil
}

// scanCandidateFiles exhausts the server-returned candidate files, or fails
// closed. A partial scan must never be mistaken for a unique symbol lookup.
func scanCandidateFiles(ctx context.Context, files []string, scan func(string) ([]symbolLocation, error)) ([]symbolLocation, error) {
	if len(files) > maxProbeFiles {
		return nil, core.NewError(core.IncompleteSearch, fmt.Sprintf("%d candidate files exceed the %d-file search budget; provide path to narrow the query", len(files), maxProbeFiles))
	}
	var hits []symbolLocation
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, core.WithCause(core.IncompleteSearch, "symbol search was interrupted; provide path to narrow the query", err)
		}
		result, err := scan(f)
		if err != nil {
			return nil, core.WithCause(core.IncompleteSearch, "failed to inspect candidate file "+f+"; provide path or retry", err)
		}
		hits = append(hits, result...)
	}
	return hits, nil
}

func (e *Engine) symbolSearchContext(ctx context.Context) (context.Context, context.CancelFunc) {
	total := e.Timeout * 4
	if total <= 0 || total > time.Minute {
		total = time.Minute
	}
	return context.WithTimeout(ctx, total)
}

// resolveSymbolPath finds every symbol matching symbolPath. When path is
// set, it searches only that file (one LSP request). Otherwise it asks
// workspace/symbol for candidate files by the path's leaf name and
// searches each of them. When the bounded search cannot finish, it errors.
func (e *Engine) resolveSymbolPath(ctx context.Context, p language.Profile, symbolPath, path string) ([]symbolLocation, []string, error) {
	segments, anchored, err := parseSymbolPath(symbolPath)
	if err != nil {
		return nil, nil, err
	}
	if path != "" {
		s, d, nodes, err := e.documentNodes(ctx, p, path)
		if err != nil {
			return nil, nil, err
		}
		return locationsFor(p, s, d, matchNodes(nodes, segments, anchored)), nil, nil
	}
	searchCtx, cancel := e.symbolSearchContext(ctx)
	defer cancel()
	leaf := segments[len(segments)-1]
	files, err := e.candidateFiles(searchCtx, p, leaf)
	if err != nil {
		return nil, nil, err
	}
	hits, err := scanCandidateFiles(searchCtx, files, func(f string) ([]symbolLocation, error) {
		profile := p
		if inferred, inferErr := language.FromPath(f); inferErr == nil && inferred.SessionKey == p.SessionKey {
			profile = inferred
		}
		s, d, nodes, err := e.documentNodes(searchCtx, profile, f)
		if err != nil {
			return nil, err
		}
		return locationsFor(profile, s, d, matchNodes(nodes, segments, anchored)), nil
	})
	return hits, nil, err
}

// resolveSymbolPathAcrossLanguages searches each configured LSP profile once
// when the caller provides only a symbol_path. Candidate files are then
// reclassified by extension so shared sessions (TypeScript/JavaScript) use
// the correct document language identifier.
func (e *Engine) resolveSymbolPathAcrossLanguages(ctx context.Context, symbolPath string) ([]symbolLocation, []string, error) {
	segments, anchored, err := parseSymbolPath(symbolPath)
	if err != nil {
		return nil, nil, err
	}
	searchCtx, cancel := e.symbolSearchContext(ctx)
	defer cancel()
	leaf := segments[len(segments)-1]
	seenSession := map[string]bool{}
	seenLocation := map[string]bool{}
	var hits []symbolLocation
	var warnings []string
	configured := 0

	for _, base := range language.Profiles {
		if seenSession[base.SessionKey] || !e.Sessions.Configured(base.SessionKey) {
			continue
		}
		seenSession[base.SessionKey] = true
		configured++
		files, err := e.candidateFiles(searchCtx, base, leaf)
		if err != nil {
			return nil, warnings, core.WithCause(core.IncompleteSearch, "could not search "+base.SessionKey+"; supply language or path", err)
		}
		matches, err := scanCandidateFiles(searchCtx, files, func(f string) ([]symbolLocation, error) {
			p := base
			if inferred, inferErr := language.FromPath(f); inferErr == nil && inferred.SessionKey == base.SessionKey {
				p = inferred
			}
			s, d, nodes, err := e.documentNodes(searchCtx, p, f)
			if err != nil {
				return nil, err
			}
			return locationsFor(p, s, d, matchNodes(nodes, segments, anchored)), nil
		})
		if err != nil {
			return nil, warnings, err
		}
		for _, hit := range matches {
			key := relativeMust(e, hit.Document.Path) + "#" + fmt.Sprint(hit.Node.SelectionRange)
			if seenLocation[key] {
				continue
			}
			seenLocation[key] = true
			hits = append(hits, hit)
		}
	}
	if configured == 0 {
		return nil, warnings, core.NewError(core.InvalidArgument, "no language server profiles are configured")
	}
	return hits, warnings, nil
}

// resolveSymbolInput chooses the narrowest safe language strategy for a
// symbol_path request. Explicit language wins, path enables extension-based
// inference, and a bare symbol_path searches all configured profiles.
func locationsFor(p language.Profile, s *session.Session, d document.Document, nodes []symbolNode) []symbolLocation {
	out := make([]symbolLocation, len(nodes))
	for i, n := range nodes {
		out[i] = symbolLocation{Profile: p, Session: s, Document: d, Node: n}
	}
	return out
}

// ambiguousError renders an AMBIGUOUS_SYMBOL error listing up to five
// candidates inline, for tools whose output shape cannot carry a
// structured candidate list.
func (e *Engine) ambiguousError(symbolPath string, hits []symbolLocation) error {
	const maxInline = 5
	names := make([]string, 0, len(hits))
	for i, h := range hits {
		if i >= maxInline {
			names = append(names, fmt.Sprintf("and %d more", len(hits)-maxInline))
			break
		}
		names = append(names, fmt.Sprintf("%s (%s)", h.Node.SymbolPath, relativeMust(e, h.Document.Path)))
	}
	return core.NewError(core.AmbiguousSymbol, fmt.Sprintf("%q matches %d symbols: %s", symbolPath, len(hits), strings.Join(names, ", ")))
}