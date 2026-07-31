package language

import (
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"strings"
)

type Profile struct {
	Name, SessionKey, LanguageID string
}

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

func ForLanguage(name string) (Profile, error) {
	name = strings.ToLower(name)
	for _, p := range Profiles {
		if p.Name == name || p.LanguageID == name {
			return p, nil
		}
	}
	return Profile{}, core.NewError(core.UnsupportedLanguage, "unsupported language")
}

func Require(name string) (Profile, error) {
	if strings.TrimSpace(name) == "" {
		return Profile{}, core.NewError(core.InvalidArgument, "language must be specified")
	}
	return ForLanguage(name)
}

func SessionKeys() []string { return []string{"python", "typescript-javascript", "go", "html", "css"} }
