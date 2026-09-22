package main

import (
	"reflect"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestBenchmarkOrderBalancesFirstPosition(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		order := benchmarkOrder(i)
		if len(order) != len(methods) || seen[order[0]] {
			t.Fatalf("unexpected order at %d: %v", i, order)
		}
		seen[order[0]] = true
	}
}

func TestSummarizeNeverTreatsFailedOrPartialAsComparable(t *testing.T) {
	samples := []measurement{
		{Name: separateCalls, ElapsedMS: 7, ResponseBytes: 100},
		{Name: separateCalls, ElapsedMS: 13, ResponseBytes: 120},
		{Name: separateCalls, ElapsedMS: 9, ResponseBytes: 110},
		{Name: symbolContext, Error: "LSP failed"},
		{Name: symbolContext, ElapsedMS: 3},
		{Name: symbolContext, ElapsedMS: 4},
		{Name: semanticSlice, Partial: true},
		{Name: semanticSlice, ElapsedMS: 2},
		{Name: semanticSlice, ElapsedMS: 3},
	}
	got := summarize(samples, 3)
	if !got[0].Comparable || got[0].MedianElapsedMS != 9 || got[0].MedianBytes != 110 {
		t.Fatalf("valid median: %+v", got[0])
	}
	if got[1].Comparable || got[1].Failures != 1 || got[2].Comparable || got[2].PartialRuns != 1 {
		t.Fatalf("incomplete runs were accepted: %+v", got)
	}
}

func TestPartialResultTracksIncompleteAndTruncated(t *testing.T) {
	for _, meta := range []core.Meta{{Complete: false}, {Complete: true, Truncated: true}, {Complete: true, SourceTruncated: true}} {
		if !partialResult(map[string]any{"meta": meta}) {
			t.Fatalf("ignored partial result: %+v", meta)
		}
	}
	if partialResult(map[string]any{"meta": core.Meta{Complete: true}}) {
		t.Fatal("complete result flagged partial")
	}
	if !partialResult(map[string]any{"meta": map[string]any{"complete": false}}) {
		t.Fatal("semantic slice metadata ignored")
	}
	if got := benchmarkOrder(3); !reflect.DeepEqual(got, methods) {
		t.Fatalf("order did not rotate back: %v", got)
	}
}
