package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestNodeKeyDiffersByRangeNotJustPath(t *testing.T) {
	a := nodeKey("f.go", core.Range{Start: core.Position{Line: 1, Column: 1}, End: core.Position{Line: 1, Column: 5}})
	b := nodeKey("f.go", core.Range{Start: core.Position{Line: 2, Column: 1}, End: core.Position{Line: 2, Column: 5}})
	if a == b {
		t.Fatal("nodeKey should differ when the range differs")
	}
	c := nodeKey("f.go", core.Range{Start: core.Position{Line: 1, Column: 1}, End: core.Position{Line: 1, Column: 5}})
	if a != c {
		t.Fatal("nodeKey should be stable for the same path and range")
	}
}

func TestImpactBudgetStopsAtRequestLimit(t *testing.T) {
	b := newImpactBudget(context.Background())
	for i := 0; i < impactMaxRequests; i++ {
		if !b.request() {
			t.Fatalf("request %d unexpectedly denied", i)
		}
	}
	if b.request() {
		t.Fatal("request should be denied once the budget is exhausted")
	}
	if !b.done() {
		t.Fatal("done() should be true once the request budget is exhausted")
	}
	if len(b.warnings) == 0 {
		t.Fatal("expected a warning recorded when the budget is exhausted")
	}
}

func TestImpactBudgetRequestDeniedAfterDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := newImpactBudget(ctx)
	if b.request() {
		t.Fatal("request should be denied once the context is done")
	}
}

func TestImpactBudgetNodeStopsAtNodeLimit(t *testing.T) {
	b := newImpactBudget(context.Background())
	for i := 0; i < impactMaxNodes; i++ {
		if !b.node() {
			t.Fatalf("node %d unexpectedly denied", i)
		}
	}
	if b.node() {
		t.Fatal("node should be denied once the node budget is exhausted")
	}
}

func TestAffectedFilesSortsDeterministically(t *testing.T) {
	direct := []any{
		map[string]any{"path": "b.go"},
		map[string]any{"path": "a.go"},
	}
	refs := []core.Location{{Path: "a.go"}, {Path: "c.go"}}
	first, _ := affectedFiles("root.go", direct, nil, refs, nil, 10)
	b, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		got, _ := affectedFiles("root.go", direct, nil, refs, nil, 10)
		gb, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if string(gb) != string(b) {
			t.Fatalf("affectedFiles is non-deterministic:\n%s\nvs\n%s", b, gb)
		}
	}
}

func TestAffectedFilesReasonsFollowFixedOrder(t *testing.T) {
	direct := []any{map[string]any{"path": "a.go"}}
	refs := []core.Location{{Path: "a.go"}}
	impls := []core.Location{{Path: "a.go"}}
	out, _ := affectedFiles("a.go", direct, nil, refs, impls, 10)
	entry := out[0].(map[string]any)
	reasons := entry["reasons"].([]string)
	want := []string{"definition", "caller", "reference", "implementation"}
	if len(reasons) != len(want) {
		t.Fatalf("reasons = %#v, want %#v", reasons, want)
	}
	for i := range want {
		if reasons[i] != want[i] {
			t.Fatalf("reasons = %#v, want %#v", reasons, want)
		}
	}
	if entry["count"] != 4 {
		t.Fatalf("count = %v, want 4", entry["count"])
	}
}

func TestAffectedFilesAppliesLimit(t *testing.T) {
	direct := []any{
		map[string]any{"path": "a.go"},
		map[string]any{"path": "b.go"},
		map[string]any{"path": "c.go"},
	}
	out, tr := affectedFiles("", direct, nil, nil, nil, 2)
	if len(out) != 2 || !tr {
		t.Fatalf("out = %#v, tr = %v", out, tr)
	}
}

func TestImpactAnalysisClampsDepth(t *testing.T) {
	for _, tc := range []struct {
		in      int
		want    int
		wantErr bool
	}{
		{0, impactDefaultDepth, false},
		{2, 2, false},
		{impactMaxDepth + 5, impactMaxDepth, false},
		{-1, 0, true},
	} {
		got, err := core.ClampLimit(tc.in, impactMaxDepth, impactDefaultDepth)
		if (err != nil) != tc.wantErr {
			t.Fatalf("depth %d: err = %v", tc.in, err)
		}
		if err == nil && got != tc.want {
			t.Fatalf("depth %d = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestImpactContextClampsToMaxTotal(t *testing.T) {
	e := &Engine{Timeout: time.Hour}
	ctx, cancel := e.impactContext(context.Background())
	defer cancel()
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected a deadline")
	}
	if time.Until(dl) > impactMaxTotal {
		t.Fatalf("deadline %v exceeds impactMaxTotal", time.Until(dl))
	}
}
