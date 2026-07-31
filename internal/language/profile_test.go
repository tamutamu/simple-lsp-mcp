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
