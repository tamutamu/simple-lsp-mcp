package tools

import (
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestEditContextAdjacentFastAPIDecoratorKeepsImportsAndBindings(t *testing.T) {
	// Real projects often have no blank line between router construction and
	// its first route. The preceding assignment must not swallow the decorator
	// or hide the imports from the agent's edit context.
	src := "import os\nfrom fastapi import APIRouter\nrouter = APIRouter()\n@router.get('/')\ndef handler():\n    return os.getcwd()\n"
	r := core.Range{Start: core.Position{Line: 5, Column: 1}, End: core.Position{Line: 7, Column: 1}}
	ctx := selectEditContext([]byte(src), r, "def handler():\n    return os.getcwd()\n", 16384)
	if ctx.Prefix != "@router.get('/')\n" {
		t.Fatalf("missing adjacent route decorator: %#v", ctx)
	}
	if !strings.Contains(ctx.Bindings, "router = APIRouter()") {
		t.Fatalf("missing adjacent module binding: %#v", ctx)
	}
	for _, expected := range []string{"import os", "from fastapi import APIRouter"} {
		if !strings.Contains(ctx.Imports, expected) {
			t.Fatalf("missing referenced import %q: %#v", expected, ctx)
		}
	}
	if ctx.Truncated {
		t.Fatalf("unexpected truncation: %#v", ctx)
	}
}

func TestEditContextAdjacentPreviousFunctionDoesNotLeakIntoDecorator(t *testing.T) {
	src := "@router.get('/old')\ndef old():\n    return 'old'\n@router.get(\n    '/new',\n)\ndef new():\n    return router\n"
	r := core.Range{Start: core.Position{Line: 7, Column: 1}, End: core.Position{Line: 9, Column: 1}}
	ctx := selectEditContext([]byte(src), r, "def new():\n    return router\n", 16384)
	want := "@router.get(\n    '/new',\n)\n"
	if ctx.Prefix != want {
		t.Fatalf("decorator crossed previous declaration: got %q, want %q", ctx.Prefix, want)
	}
}

func TestEditContextAdjacentPlainFunctionStillFindsImports(t *testing.T) {
	src := "import os\ndef previous():\n    return 1\ndef target():\n    return os.getcwd()\n"
	r := core.Range{Start: core.Position{Line: 4, Column: 1}, End: core.Position{Line: 6, Column: 1}}
	ctx := selectEditContext([]byte(src), r, "def target():\n    return os.getcwd()\n", 16384)
	if ctx.Prefix != "" {
		t.Fatalf("previous function was included as a decorator: %#v", ctx)
	}
	if !strings.Contains(ctx.Imports, "import os") {
		t.Fatalf("lost import without a separating blank line: %#v", ctx)
	}
}
