package tools

import (
	"encoding/json"
	"testing"
)

func TestIsNullJSON(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"", true},
		{"null", true},
		{" null ", true},
		{`{"kind":"markdown","value":"x"}`, false},
		{`"x"`, false},
	} {
		if got := isNullJSON(json.RawMessage(tc.raw)); got != tc.want {
			t.Fatalf("isNullJSON(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestHoverTextMarkupContentMarkdown(t *testing.T) {
	text, kind := hoverText(json.RawMessage(`{"kind":"markdown","value":"**bold**"}`))
	if text != "**bold**" || kind != "markdown" {
		t.Fatalf("text=%q kind=%q", text, kind)
	}
}

func TestHoverTextMarkupContentPlaintext(t *testing.T) {
	text, kind := hoverText(json.RawMessage(`{"kind":"plaintext","value":"plain"}`))
	if text != "plain" || kind != "plaintext" {
		t.Fatalf("text=%q kind=%q", text, kind)
	}
}

func TestHoverTextBareString(t *testing.T) {
	text, kind := hoverText(json.RawMessage(`"hello"`))
	if text != "hello" || kind != "markdown" {
		t.Fatalf("text=%q kind=%q", text, kind)
	}
}

func TestHoverTextMarkedStringObject(t *testing.T) {
	text, kind := hoverText(json.RawMessage(`{"language":"go","value":"func f()"}`))
	if text != "```go\nfunc f()\n```" || kind != "markdown" {
		t.Fatalf("text=%q kind=%q", text, kind)
	}
}

func TestHoverTextMarkedStringArrayMixed(t *testing.T) {
	text, kind := hoverText(json.RawMessage(`[{"language":"go","value":"func f()"},"docs here"]`))
	want := "```go\nfunc f()\n```\n\ndocs here"
	if text != want || kind != "markdown" {
		t.Fatalf("text=%q kind=%q, want %q", text, kind, want)
	}
}

func TestHoverTextEmptyArray(t *testing.T) {
	text, kind := hoverText(json.RawMessage(`[]`))
	if text != "" || kind != "markdown" {
		t.Fatalf("text=%q kind=%q", text, kind)
	}
}

func TestHoverTextNull(t *testing.T) {
	text, kind := hoverText(json.RawMessage(`null`))
	if text != "" || kind != "" {
		t.Fatalf("text=%q kind=%q", text, kind)
	}
}

func TestHoverTextInvalidJSON(t *testing.T) {
	text, kind := hoverText(json.RawMessage(`{not json`))
	if text != "" || kind != "" {
		t.Fatalf("text=%q kind=%q", text, kind)
	}
}

func TestMarkedStringBareString(t *testing.T) {
	if got := markedString(json.RawMessage(`"plain text"`)); got != "plain text" {
		t.Fatalf("markedString = %q", got)
	}
}

func TestMarkedStringObjectWithoutLanguage(t *testing.T) {
	if got := markedString(json.RawMessage(`{"language":"","value":"just text"}`)); got != "just text" {
		t.Fatalf("markedString = %q", got)
	}
}
