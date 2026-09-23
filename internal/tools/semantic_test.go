package tools

import "testing"

func TestSemanticByteLimitDefaultsAndValidates(t *testing.T) {
	if _, err := semanticByteLimit(map[string]any{"max_tokens": 6000}); err == nil {
		t.Fatal("legacy max_tokens should be rejected, not silently ignored")
	}
	got, err := semanticByteLimit(map[string]any{})
	if err != nil || got != semanticDefaultBytes {
		t.Fatalf("default max_bytes = %d, %v", got, err)
	}
	got, err = semanticByteLimit(map[string]any{"max_bytes": float64(12000)})
	if err != nil || got != 12000 {
		t.Fatalf("explicit max_bytes = %d, %v", got, err)
	}
	if _, err := semanticByteLimit(map[string]any{"max_bytes": float64(semanticMinBytes - 1)}); err == nil {
		t.Fatal("expected max_bytes below minimum to fail")
	}
	if _, err := semanticByteLimit(map[string]any{"max_bytes": float64(semanticMaxBytes + 1)}); err == nil {
		t.Fatal("expected max_bytes above hard cap to fail")
	}
}

func TestTruncateUTF8BytesKeepsValidUTF8(t *testing.T) {
	got, truncated := truncateUTF8Bytes("abc日本語", 5)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if got != "abc" {
		t.Fatalf("got %q, want %q", got, "abc")
	}
}

func TestSemanticCallKeyIsStableByLocation(t *testing.T) {
	call := map[string]any{"path": "a.go", "line": 12, "name": "run", "symbol_id": "random"}
	if got := semanticCallKey(call); got != "a.go#12:run" {
		t.Fatalf("semanticCallKey = %q", got)
	}
}

func TestEnforceSemanticBudgetCompactsLargeSources(t *testing.T) {
	result := map[string]any{
		"root": map[string]any{"name": "root", "source": string(make([]byte, 5000))},
		"dependencies": []any{
			map[string]any{"name": "a", "source": string(make([]byte, 5000))},
		},
		"dependents": []any{map[string]any{"name": "caller"}},
		"meta":       map[string]any{"max_bytes": 2048},
	}
	if !enforceSemanticBudget(result, 2048) {
		t.Fatal("expected result to be compacted")
	}
	if got := encodedSize(result); got > 2048 {
		t.Fatalf("compacted result is %d bytes, exceeds budget", got)
	}
}

func TestRelatedTestsRequiresLSPReference(t *testing.T) {
	refs := []any{
		map[string]any{"path": "internal/user/user_test.go", "lines": []int{12}},
		map[string]any{"path": "internal/user/user.go", "lines": []int{4}},
		map[string]any{"path": "src/user.spec.ts", "lines": []int{8}},
	}
	got := semanticRelatedTests(refs, 8)
	if len(got) != 2 {
		t.Fatalf("related tests = %#v", got)
	}
	if got[0].(map[string]any)["reason"] != "symbol_reference" {
		t.Fatalf("missing provenance: %#v", got[0])
	}
	if semanticTestFile("src/application.ts") {
		t.Fatal("ordinary source must not be a test")
	}
}

func TestSemanticBudgetErrorsIfIdentityCannotFit(t *testing.T) {
	oversized := map[string]any{"root": map[string]any{"symbol_path": string(make([]byte, 10000))}, "meta": map[string]any{"max_bytes": 2048}}
	enforceSemanticBudget(oversized, 2048)
	if encodedSize(oversized) <= 2048 {
		t.Fatal("test identity is supposed to be oversized")
	}
}
