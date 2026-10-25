package tools

import (
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestEditContextFastAPIRouteSelectsOnlyReferencedModuleEvidence(t *testing.T) {
	src := strings.Join([]string{
		"import uuid", "import os", "from typing import Any",
		"from fastapi import APIRouter, HTTPException",
		"from sqlmodel import select",
		"from app.models import (", "    Item,", "    ItemsPublic,", "    UnusedModel,", ")",
		"router = APIRouter(prefix='/items')",
		"OTHER = 'unrelated value'", "",
		"@router.get(", "    '/',", "    response_model=ItemsPublic,", ")",
		"def read_items(session: SessionDep) -> Any:",
		"    result = select(Item)",
		"    return ItemsPublic(data=result)",
		"",
	}, "\n")
	r := core.Range{Start: core.Position{Line: 18, Column: 1}, End: core.Position{Line: 21, Column: 1}}
	code := "def read_items(session: SessionDep) -> Any:\n    result = select(Item)\n    return ItemsPublic(data=result)\n"
	ctx := selectEditContext([]byte(src), r, code, 16*1024)
	if ctx.Truncated {
		t.Fatalf("unexpected truncation: %#v", ctx)
	}
	if !strings.HasPrefix(ctx.Prefix, "@router.get(") || !strings.Contains(ctx.Prefix, "response_model=ItemsPublic") {
		t.Errorf("route semantics lost: %q", ctx.Prefix)
	}
	for _, need := range []string{"from typing import Any", "from sqlmodel import select", "from app.models import", "ItemsPublic"} {
		if !strings.Contains(ctx.Imports, need) {
			t.Errorf("missing referenced import %q from %q", need, ctx.Imports)
		}
	}
	for _, omit := range []string{"import os", "import uuid", "OTHER"} {
		if strings.Contains(ctx.Imports, omit) || strings.Contains(ctx.Bindings, omit) {
			t.Errorf("unrelated import/binding included %q", omit)
		}
	}
	if !strings.Contains(ctx.Imports, "from fastapi import APIRouter") {
		t.Errorf("missing transitive import used by router binding: %#v", ctx)
	}
	if !strings.Contains(ctx.Bindings, "router = APIRouter") {
		t.Errorf("related module configuration missing: %#v", ctx)
	}
}

func TestEditContextDoesNotAccidentallyIncludePrecedingDeclaration(t *testing.T) {
	src := "from typing import Any\n\n@router.get('/a')\ndef a():\n    return 'A'\n\n@router.get('/b')\ndef b() -> Any:\n    return 'B'\n"
	r := core.Range{Start: core.Position{Line: 8, Column: 1}, End: core.Position{Line: 10, Column: 1}}
	ctx := selectEditContext([]byte(src), r, "def b() -> Any:\n    return 'B'\n", 16384)
	if ctx.Prefix != "@router.get('/b')\n" || strings.Contains(ctx.Prefix, "a()") {
		t.Fatalf("crossed declaration boundaries: %#v", ctx)
	}
}

func TestEditContextTypescriptImportsAndNoUnrelatedCode(t *testing.T) {
	src := strings.Join([]string{
		"import { useSuspenseQuery } from \"@tanstack/react-query\"",
		"import { Search } from \"lucide-react\"",
		"import { Suspense } from \"react\"",
		"import PendingItems from \"@/components/Pending/PendingItems\"",
		"const unrelated = 999",
		"",
		"function ItemsTable() {",
		"  return <Suspense fallback={<PendingItems />}>Loading</Suspense>",
		"}",
		"",
	}, "\n")
	r := core.Range{Start: core.Position{Line: 7, Column: 1}, End: core.Position{Line: 10, Column: 1}}
	ctx := selectEditContext([]byte(src), r, "function ItemsTable() {\n  return <Suspense fallback={<PendingItems />}>Loading</Suspense>\n}\n", 16384)
	if !strings.Contains(ctx.Imports, "import { Suspense }") || !strings.Contains(ctx.Imports, "import PendingItems") {
		t.Errorf("missing TS imports: %q", ctx.Imports)
	}
	if strings.Contains(ctx.Imports, "useSuspenseQuery") || strings.Contains(ctx.Imports, "lucide-react") {
		t.Errorf("unrelated TS imports: %q", ctx.Imports)
	}
	if ctx.Bindings != "" {
		t.Errorf("unrelated bindings: %q", ctx.Bindings)
	}
}

func TestEditContextBoundsAndNoFalseSubstringMatches(t *testing.T) {
	if containsIdentifier("ItemPublic", "Item") || !containsIdentifier("select(Item)", "Item") {
		t.Fatal("substring matches confuse distinct symbols")
	}
	src := "from x import Relevant\nfrom y import RelevantExtra\n\n@api.route('/test')\ndef f():\n    return Relevant\n"
	r := core.Range{Start: core.Position{Line: 5, Column: 1}, End: core.Position{Line: 7, Column: 1}}
	ctx := selectEditContext([]byte(src), r, "def f():\n    return Relevant\n", 16384)
	if !strings.Contains(ctx.Imports, "from x") || strings.Contains(ctx.Imports, "from y") {
		t.Fatalf("incorrect identifiers: %q", ctx.Imports)
	}
	tooLongPrefix := "@" + strings.Repeat("x", 8000) + "\ndef f():\n    return 1\n"
	r = core.Range{Start: core.Position{Line: 2, Column: 1}, End: core.Position{Line: 4, Column: 1}}
	ctx = selectEditContext([]byte(tooLongPrefix), r, "def f():\n    return 1\n", 600)
	if !ctx.Truncated || ctx.Prefix != "" {
		t.Errorf("oversize decorator included: %#v", ctx)
	}
}

func TestEditContextSkipsLocalImportsAndLocalAssignments(t *testing.T) {
	source := "import os\n\ndef other():\n    import uuid\n    token = make_token()\n    return token\n\n@router.get('/t')\ndef handler():\n    return token + uuid + os.getcwd()\n"
	r := core.Range{Start: core.Position{Line: 9, Column: 1}, End: core.Position{Line: 11, Column: 1}}
	c := selectEditContext([]byte(source), r, "def handler():\n    return token + uuid + os.getcwd()\n", 16384)
	if strings.Contains(c.Imports, "import uuid") || strings.Contains(c.Bindings, "token =") {
		t.Fatalf("included local scope as module context: %#v", c)
	}
	if !strings.Contains(c.Imports, "import os") {
		t.Fatalf("lost referenced root import: %#v", c)
	}
}

func TestEditContextGoImports(t *testing.T) {
	for _, tt := range []struct {
		importStatement, usage string
		want                   bool
	}{
		{"import \"fmt\"", "fmt.Sprintf(\"x\")", true},
		{"import \"strings\"", "fmt.Sprintf(\"x\")", false},
		{"import (\n    \"fmt\"\n    \"strings\"\n)", "fmt.Println(\"hello\")", true},
	} {
		if got := importIsReferenced(tt.importStatement, tt.usage); got != tt.want {
			t.Errorf("import %q usage %q -> %t, want %t", tt.importStatement, tt.usage, got, tt.want)
		}
	}
}
