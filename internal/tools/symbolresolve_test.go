package tools

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestCandidateScanExhaustsBeyondFormerEightFileLimit(t *testing.T) {
	files := make([]string, 9)
	for i := range files {
		files[i] = fmt.Sprintf("file-%d.go", i)
	}
	visited := 0
	hits, err := scanCandidateFiles(context.Background(), files, func(f string) ([]symbolLocation, error) {
		visited++
		if f == files[8] {
			return []symbolLocation{{Node: symbolNode{Name: "target"}}}, nil
		}
		return nil, nil
	})
	if err != nil || visited != 9 || len(hits) != 1 || hits[0].Node.Name != "target" {
		t.Fatalf("visited=%d hits=%#v err=%v", visited, hits, err)
	}
}

func TestCandidateScanFailsClosedWhenIncomplete(t *testing.T) {
	files := make([]string, maxProbeFiles+1)
	calls := 0
	_, err := scanCandidateFiles(context.Background(), files, func(string) ([]symbolLocation, error) {
		calls++
		return nil, nil
	})
	var appErr *core.AppError
	if !errors.As(err, &appErr) || appErr.Code != core.IncompleteSearch || calls != 0 {
		t.Fatalf("error=%v calls=%d; want INCOMPLETE_SEARCH and no calls", err, calls)
	}
	_, err = scanCandidateFiles(context.Background(), []string{"a.go", "b.go"}, func(f string) ([]symbolLocation, error) {
		if f == "b.go" {
			return nil, errors.New("language server unavailable")
		}
		return []symbolLocation{{Node: symbolNode{Name: "match"}}}, nil
	})
	if !errors.As(err, &appErr) || appErr.Code != core.IncompleteSearch {
		t.Fatalf("error=%v; want INCOMPLETE_SEARCH rather than partial hit", err)
	}
}