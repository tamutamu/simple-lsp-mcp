package language

import (
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

// SessionKeys returns the configured language-server profile names.
func SessionKeys() []string {
	return []string{"python", "typescript-javascript", "go", "html", "css"}
}
