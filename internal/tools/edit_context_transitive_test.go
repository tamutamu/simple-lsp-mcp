package tools

import (
    "strings"
    "testing"

    "github.com/tamutamu/simple-lsp-mcp/internal/core"
)

// The route directly uses router, but router depends on prefix, and prefix
// depends on BASE. All three assignments precede the route. The old forward
// scan selected only router and forced an agent to reopen the file to discover
// the missing configuration chain.
func TestEditContextIncludesTransitiveEarlierModuleBindings(t *testing.T) {
    source := strings.Join([]string{
        "from fastapi import APIRouter",
        "from app.settings import make_settings",
        "BASE = make_settings()",
        "prefix = BASE.api_prefix",
        "router = APIRouter(prefix=prefix)",
        "unrelated = 42",
        "",
        "@router.get('/items')",
        "def list_items():",
        "    return []",
        "",
    }, "\n")
    code := "def list_items():\n    return []\n"
    r := core.Range{Start: core.Position{Line: 9, Column: 1}, End: core.Position{Line: 11, Column: 1}}
    got := selectEditContext([]byte(source), r, code, 16*1024)
    if got.Truncated {
        t.Fatalf("unexpected truncation: %+v", got)
    }
    want := "BASE = make_settings()\nprefix = BASE.api_prefix\nrouter = APIRouter(prefix=prefix)\n"
    if got.Bindings != want {
        t.Fatalf("missing or out-of-order transitive bindings: got %q, want %q", got.Bindings, want)
    }
    if !strings.Contains(got.Imports, "from fastapi import APIRouter") ||
        !strings.Contains(got.Imports, "from app.settings import make_settings") {
        t.Fatalf("imports used by transitive bindings missing: %q", got.Imports)
    }
    if strings.Contains(got.Bindings, "unrelated") {
        t.Fatalf("unrelated module assignment leaked: %q", got.Bindings)
    }
}

// Mixed declaration ordering must terminate, select each binding once, and
// keep the result in source order rather than dependency-discovery order.
func TestEditContextTransitiveBindingsMixedOrdering(t *testing.T) {
    source := strings.Join([]string{
        "const root = createRoot()",
        "const mid = wrap(root)",
        "const late = makeLate()",
        "const entry = combine(mid, late)",
        "const unused = 1",
        "",
        "function handler() {",
        "  return entry",
        "}",
        "",
    }, "\n")
    r := core.Range{Start: core.Position{Line: 7, Column: 1}, End: core.Position{Line: 10, Column: 1}}
    got := selectEditContext([]byte(source), r, "function handler() {\n  return entry\n}\n", 16*1024)
    want := "const root = createRoot()\nconst mid = wrap(root)\nconst late = makeLate()\nconst entry = combine(mid, late)\n"
    if got.Bindings != want || got.Truncated {
        t.Fatalf("transitive context incorrect: %+v; want bindings %q", got, want)
    }
}
