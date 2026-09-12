package tools

import "testing"

func TestSemanticTokenLimitDefaultsAndValidates(t *testing.T) {
	got, err := semanticTokenLimit(map[string]any{})
	if err != nil || got != semanticDefaultTokens {
		t.Fatalf("default max_tokens = %d, %v", got, err)
	}
	got, err = semanticTokenLimit(map[string]any{"max_tokens": float64(1200)})
	if err != nil || got != 1200 {
		t.Fatalf("explicit max_tokens = %d, %v", got, err)
	}
	if _, err := semanticTokenLimit(map[string]any{"max_tokens": float64(semanticMinTokens - 1)}); err == nil {
		t.Fatal("expected max_tokens below minimum to fail")
	}
	if _, err := semanticTokenLimit(map[string]any{"max_tokens": float64(semanticMaxTokens + 1)}); err == nil {
		t.Fatal("expected max_tokens above hard cap to fail")
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
		"meta":       map[string]any{"max_tokens": 512},
	}
	if !enforceSemanticBudget(result, 512*4) {
		t.Fatal("expected result to be compacted")
	}
	if got := encodedSize(result); got > 512*4 {
		t.Fatalf("compacted result is %d bytes, exceeds budget", got)
	}
}
