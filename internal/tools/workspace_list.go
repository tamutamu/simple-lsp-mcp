package tools

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
)

const (
	workspaceListPageSize  = 100
	workspaceListFileBatch = 32
)

// A cursor identifies the next symbol in an ordered file list. No persistent
// workspace index or source-code snapshot is maintained between requests.
type workspaceListCursor struct {
	Scope string `json:"scope"`
	File  int    `json:"file"`
	Index int    `json:"index"`
	Hash  string `json:"hash,omitempty"`
}

var workspaceListExcludedDirs = map[string]bool{
	".git": true, "node_modules": true, ".venv": true, "venv": true,
	"__pycache__": true, ".next": true, ".cache": true,
	"dist": true, "build": true, "target": true, "coverage": true,
}

// ListWorkspaceSymbols enumerates symbols from documentSymbol in source files,
// not workspace/symbol with an empty query (which LSP servers need not support).
// Each page is limited by both symbols and files, so empty or huge workspaces
// cannot trigger unbounded LSP requests in a single call.
func (e *Engine) ListWorkspaceSymbols(ctx context.Context, in map[string]any) (map[string]any, error) {
	if _, exists := in["query"]; exists {
		return nil, core.NewError(core.InvalidArgument, "list_workspace_symbols does not search by query; use search_symbols")
	}
	pageSize := min(workspaceListPageSize, max(1, e.MaxResults))
	limit, err := core.ClampLimit(intVal(in, "limit"), pageSize, pageSize)
	if err != nil {
		return nil, err
	}
	var selected language.Profile
	if name := stringVal(in, "language"); name != "" {
		selected, err = language.Require(name)
		if err != nil {
			return nil, err
		}
		if !e.Sessions.Configured(selected.SessionKey) {
			return nil, core.NewError(core.UnsupportedLanguage, "no language server is configured for "+name)
		}
	}
	if selected.Name == "" {
		configured := false
		for _, key := range language.SessionKeys() {
			configured = configured || e.Sessions.Configured(key)
		}
		if !configured {
			return nil, core.NewError(core.UnsupportedLanguage, "no language servers are configured")
		}
	}
	if value, exists := in["cursor"]; exists {
		if _, ok := value.(string); !ok {
			return nil, core.NewError(core.InvalidArgument, "cursor must be a string")
		}
	}
	if _, exists := in["kinds"]; exists {
		if _, ok := in["kinds"].([]any); !ok {
			return nil, core.NewError(core.InvalidArgument, "kinds must be an array of strings")
		}
	}
	files, err := e.workspaceListFiles(ctx, selected.Name)
	if err != nil {
		return nil, err
	}
	// Binding the cursor to the sorted file list and filters prevents silently
	// resuming at the wrong position after renames or different query options.
	scopeInput, _ := json.Marshal(struct {
		Language string   `json:"language"`
		Kinds    any      `json:"kinds"`
		Files    []string `json:"files"`
	}{selected.Name, in["kinds"], files})
	scopeHash := sha256.Sum256(scopeInput)
	scope := hex.EncodeToString(scopeHash[:])
	position := workspaceListCursor{Scope: scope}
	if raw := stringVal(in, "cursor"); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || json.Unmarshal(decoded, &position) != nil || position.Scope != scope ||
			position.File < 0 || position.File >= len(files) || position.Index < 0 ||
			(position.Index > 0 && position.Hash == "") {
			return nil, core.NewError(core.InvalidArgument, "invalid or stale cursor; restart without cursor")
		}
	}
	out := make([]core.SymbolSummary, 0, limit)
	i := position.File
	for inspected := 0; i < len(files) && inspected < workspaceListFileBatch; i++ {
		if err := ctx.Err(); err != nil {
			return nil, core.WithCause(core.IncompleteSearch, "workspace enumeration interrupted; retry with the same cursor", err)
		}
		path := files[i]
		p, _ := language.FromPath(path) // workspaceListFiles validated the extension.
		_, doc, nodes, err := e.documentNodes(ctx, p, path)
		if err != nil {
			return nil, core.WithCause(core.IncompleteSearch, "failed to enumerate "+path+"; retry with the same cursor", err)
		}
		if i == position.File && position.Index > 0 && doc.Hash != position.Hash {
			return nil, core.NewError(core.InvalidArgument, "file changed since cursor was issued; restart without cursor")
		}
		flat := flattenWorkspaceNodes(nodes, in)
		start := 0
		if i == position.File {
			start = position.Index
			if start > len(flat) {
				return nil, core.NewError(core.InvalidArgument, "symbol positions changed since cursor was issued; restart without cursor")
			}
		}
		inspected++
		for j := start; j < len(flat); j++ {
			n := flat[j]
			id := e.registerNode(p, path, doc.URI, doc.Hash, n)
			out = append(out, core.SymbolSummary{
				SymbolID: id, SymbolPath: n.SymbolPath, Name: n.Name,
				Kind: n.Kind, ContainerName: n.ContainerName, Language: p.Name,
				Path: path, Range: n.Range, SelectionRange: n.SelectionRange,
			})
			if len(out) == limit {
				next := workspaceListCursor{Scope: scope, File: i, Index: j + 1, Hash: doc.Hash}
				if j+1 == len(flat) {
					next.File++
					next.Index = 0
					next.Hash = ""
				}
				return workspaceListPage(out, next, len(files)), nil
			}
		}
	}
	return workspaceListPage(out, workspaceListCursor{Scope: scope, File: i}, len(files)), nil
}

func workspaceListPage(symbols []core.SymbolSummary, next workspaceListCursor, totalFiles int) map[string]any {
	hasMore := next.File < totalFiles
	out := map[string]any{
		"symbols": symbols,
		"meta":    core.Meta{Complete: !hasMore, Truncated: hasMore},
	}
	if hasMore {
		data, _ := json.Marshal(next)
		out["next_cursor"] = base64.RawURLEncoding.EncodeToString(data)
	}
	return out
}

func flattenWorkspaceNodes(nodes []symbolNode, in map[string]any) []symbolNode {
	var out []symbolNode
	var visit func([]symbolNode)
	visit = func(children []symbolNode) {
		for _, n := range children {
			if kindAllowed(n.Kind, in) {
				out = append(out, n)
			}
			visit(n.Children)
		}
	}
	visit(nodes)
	return out
}

func (e *Engine) workspaceListFiles(ctx context.Context, selectedLanguage string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(e.WS.Root(), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != e.WS.Root() && workspaceListExcludedDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() { // Do not follow symlinks out of the workspace.
			return nil
		}
		rel, err := filepath.Rel(e.WS.Root(), path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		p, err := language.FromPath(rel)
		if err != nil || !e.Sessions.Configured(p.SessionKey) || (selectedLanguage != "" && p.Name != selectedLanguage) {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, core.WithCause(core.IncompleteSearch, fmt.Sprintf("could not enumerate workspace files: %s", strings.TrimSpace(err.Error())), err)
	}
	// filepath.WalkDir walks lexically; retain this ordering across pages.
	return files, nil
}
