package language

import "testing"

func TestRequireReturnsExplicitLanguage(t *testing.T) {
	p, err := Require("python")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "python" || p.SessionKey != "python" || p.LanguageID != "python" {
		t.Fatalf("profile = %#v", p)
	}
}

func TestRequireRejectsAnOmittedLanguage(t *testing.T) {
	if _, err := Require(""); err == nil {
		t.Fatal("Require(\"\") returned nil error")
	}
}

func TestFromPathInfersSupportedExtensions(t *testing.T) {
	cases := map[string]string{
		"main.go":    "go",
		"app.ts":     "typescript",
		"view.tsx":   "typescriptreact",
		"app.js":     "javascript",
		"view.jsx":   "javascriptreact",
		"main.py":    "python",
		"index.html": "html",
		"style.css":  "css",
	}
	for path, want := range cases {
		p, err := FromPath(path)
		if err != nil || p.Name != want {
			t.Fatalf("FromPath(%q) = %#v, %v; want %q", path, p, err, want)
		}
	}
	if _, err := FromPath("README.md"); err == nil {
		t.Fatal("expected unknown extension to require explicit language")
	}
}
