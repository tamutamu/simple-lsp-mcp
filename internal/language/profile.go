package language

import (
	"path/filepath"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

// Profile maps an MCP language name to an LSP session and language identifier.
type Profile struct {
	Name, SessionKey, LanguageID string
}

// Profiles lists every language accepted by the MCP tools.
var Profiles = []Profile{
	{Name: "python", SessionKey: "python", LanguageID: "python"},
	{Name: "typescript", SessionKey: "typescript-javascript", LanguageID: "typescript"},
	{Name: "typescriptreact", SessionKey: "typescript-javascript", LanguageID: "typescriptreact"},
	{Name: "javascript", SessionKey: "typescript-javascript", LanguageID: "javascript"},
	{Name: "javascriptreact", SessionKey: "typescript-javascript", LanguageID: "javascriptreact"},
	{Name: "go", SessionKey: "go", LanguageID: "go"},
	{Name: "html", SessionKey: "html", LanguageID: "html"},
	{Name: "css", SessionKey: "css", LanguageID: "css"},
}

// ForLanguage returns the profile matching a user-provided language name.
func ForLanguage(name string) (Profile, error) {
	name = strings.ToLower(name)
	for _, p := range Profiles {
		if p.Name == name || p.LanguageID == name {
			return p, nil
		}
	}
	return Profile{}, core.NewError(core.UnsupportedLanguage, "unsupported language")
}

// Require validates that a language argument was supplied and is supported.
func Require(name string) (Profile, error) {
	if strings.TrimSpace(name) == "" {
		return Profile{}, core.NewError(core.InvalidArgument, "language must be specified")
	}
	return ForLanguage(name)
}

// FromPath infers an MCP language profile from a source file extension.
// An explicit language supplied by the caller should always take precedence.
func FromPath(path string) (Profile, error) {
	ext := strings.ToLower(filepath.Ext(path))
	name := map[string]string{
		".py": "python",
		".ts": "typescript", ".mts": "typescript", ".cts": "typescript",
		".tsx": "typescriptreact",
		".js":  "javascript", ".mjs": "javascript", ".cjs": "javascript",
		".jsx":  "javascriptreact",
		".go":   "go",
		".html": "html", ".htm": "html",
		".css": "css",
	}[ext]
	if name == "" {
		return Profile{}, core.NewError(core.InvalidArgument, "language could not be inferred from path; specify language")
	}
	return ForLanguage(name)
}

// ForSessionKey resolves a stored LSP session key back to the most precise
// language profile possible, using the source path to disambiguate shared
// sessions such as TypeScript/JavaScript.
func ForSessionKey(key, path string) (Profile, error) {
	if path != "" {
		if p, err := FromPath(path); err == nil && p.SessionKey == key {
			return p, nil
		}
	}
	for _, p := range Profiles {
		if p.SessionKey == key {
			return p, nil
		}
	}
	return Profile{}, core.NewError(core.UnsupportedLanguage, "unsupported language server profile")
}

// SessionKeys returns the configured language-server profile names.
func SessionKeys() []string {
	return []string{"python", "typescript-javascript", "go", "html", "css"}
}
