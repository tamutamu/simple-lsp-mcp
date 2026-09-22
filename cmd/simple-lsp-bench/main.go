package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/tools"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

const (
	separateCalls = "separate_navigation_calls"
	symbolContext = "get_symbol_context"
	semanticSlice = "get_semantic_slice"
)

var methods = []string{separateCalls, symbolContext, semanticSlice}

type measurement struct {
	Name          string `json:"name"`
	Run           int    `json:"run"`
	ToolCalls     int    `json:"tool_calls"`
	ElapsedMS     int64  `json:"elapsed_ms"`
	ResponseBytes int    `json:"response_bytes"`
	Partial       bool   `json:"partial,omitempty"`
	Error         string `json:"error,omitempty"`
}

type summary struct {
	Name            string `json:"name"`
	Runs            int    `json:"runs"`
	Failures        int    `json:"failures"`
	PartialRuns     int    `json:"partial_runs"`
	MedianElapsedMS int64  `json:"median_elapsed_ms"`
	MedianBytes     int    `json:"median_response_bytes"`
	Comparable      bool   `json:"comparable"`
}

func main() {
	var root, symbolPath, path, language string
	var depth, maxBytes, runs, warmups int
	var timeout time.Duration
	flag.StringVar(&root, "workspace", ".", "workspace root")
	flag.StringVar(&symbolPath, "symbol", "", "symbol_path to benchmark")
	flag.StringVar(&path, "path", "", "optional workspace-relative source path")
	flag.StringVar(&language, "language", "", "optional language override")
	flag.IntVar(&depth, "depth", 1, "semantic-slice dependency depth")
	flag.IntVar(&maxBytes, "max-bytes", 24576, "semantic-slice maximum serialized JSON bytes")
	flag.IntVar(&runs, "runs", 3, "number of recorded runs per method (1-100)")
	flag.IntVar(&warmups, "warmups", 1, "unrecorded warm-up cycles per method (0-20)")
	flag.DurationVar(&timeout, "timeout", 3*time.Minute, "total benchmark timeout")
	flag.Parse()
	if symbolPath == "" || runs < 1 || runs > 100 || warmups < 0 || warmups > 20 {
		fmt.Fprintln(os.Stderr, "--symbol and valid --runs (1-100), --warmups (0-20) are required")
		os.Exit(2)
	}

	ws, err := workspace.Open(root)
	if err != nil {
		fatal(err)
	}
	cfg, err := config.Load(config.Runtime{Workspace: ws.Root(), RequestTimeout: 15 * time.Second, DiagnosticsWait: 2 * time.Second, MaxResults: 500})
	if err != nil {
		fatal(err)
	}
	engine := tools.New(ws, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		engine.Sessions.Shutdown(shutdownCtx)
	}()

	target := map[string]any{"symbol_path": symbolPath}
	if path != "" {
		target["path"] = path
	}
	if language != "" {
		target["language"] = language
	}
	invoke := func(name string) measurement {
		switch name {
		case separateCalls:
			return measureRaw(ctx, engine, target)
		case symbolContext:
			return measureOne(name, ctx, func() (map[string]any, error) {
				return engine.SymbolContext(ctx, target)
			})
		case semanticSlice:
			return measureOne(name, ctx, func() (map[string]any, error) {
				in := copyTarget(target)
				in["depth"] = depth
				in["max_bytes"] = maxBytes
				return engine.SemanticSlice(ctx, in)
			})
		default:
			panic("unknown benchmark method")
		}
	}

	// All measured calls run against a warmed LSP. Rotate which method runs
	// first, so the earlier method does not get all the cold-cache penalty.
	var warmupErrors []string
	for cycle := 0; cycle < warmups; cycle++ {
		for _, name := range benchmarkOrder(cycle) {
			if sample := invoke(name); sample.Error != "" {
				warmupErrors = append(warmupErrors, name+": "+sample.Error)
			}
		}
	}
	results := make([]measurement, 0, runs*len(methods))
	for run := 0; run < runs; run++ {
		for _, name := range benchmarkOrder(run) {
			sample := invoke(name)
			sample.Run = run + 1
			results = append(results, sample)
		}
	}
	summaries := summarize(results, runs)
	out := map[string]any{
		"target": target, "warmup_cycles": warmups, "warmup_errors": warmupErrors,
		"runs_per_method": runs, "results": results, "summary": summaries,
		"methodology": "shared warmed LSP; rotated method order; partial/failed runs are not comparable; no model-token estimates or agent-success claims",
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fatal(err)
	}
	for _, entry := range summaries {
		if !entry.Comparable {
			os.Exit(1)
		}
	}
	if len(warmupErrors) != 0 {
		os.Exit(1)
	}
}

func benchmarkOrder(run int) []string {
	out := make([]string, 0, len(methods))
	for i := range methods {
		out = append(out, methods[(run+i)%len(methods)])
	}
	return out
}

func summarize(samples []measurement, runs int) []summary {
	out := make([]summary, 0, len(methods))
	for _, name := range methods {
		entry := summary{Name: name, Runs: runs}
		var times, sizes []int
		for _, sample := range samples {
			if sample.Name != name {
				continue
			}
			if sample.Error != "" {
				entry.Failures++
				continue
			}
			if sample.Partial {
				entry.PartialRuns++
				continue
			}
			times = append(times, int(sample.ElapsedMS))
			sizes = append(sizes, sample.ResponseBytes)
		}
		entry.Comparable = entry.Failures == 0 && entry.PartialRuns == 0 && len(times) == runs
		if len(times) > 0 {
			entry.MedianElapsedMS = int64(median(times))
			entry.MedianBytes = median(sizes)
		}
		out = append(out, entry)
	}
	return out
}

func median(values []int) int {
	sort.Ints(values)
	if len(values)%2 == 1 {
		return values[len(values)/2]
	}
	return (values[len(values)/2-1] + values[len(values)/2]) / 2
}

func measureRaw(ctx context.Context, engine *tools.Engine, target map[string]any) measurement {
	start := time.Now()
	parts := make([]any, 0, 5)
	findIn := copyTarget(target)
	findIn["include_source"] = true
	found, err := engine.FindSymbol(ctx, findIn)
	if err != nil {
		return failed(separateCalls, 1, start, err)
	}
	parts = append(parts, found)
	symbol, _ := found["symbol"].(map[string]any)
	id, _ := symbol["symbol_id"].(string)
	if id == "" {
		return failed(separateCalls, 1, start, fmt.Errorf("find_symbol did not return one symbol"))
	}
	in := map[string]any{"symbol_id": id}
	calls := []func() (map[string]any, error){
		func() (map[string]any, error) { return engine.Hierarchy(ctx, "get_incoming_calls", in) },
		func() (map[string]any, error) { return engine.Hierarchy(ctx, "get_outgoing_calls", in) },
		func() (map[string]any, error) {
			return engine.Relationship(ctx, "find_references", "textDocument/references", "references", in)
		},
		func() (map[string]any, error) {
			return engine.Relationship(ctx, "find_implementations", "textDocument/implementation", "implementation", in)
		},
	}
	for i, call := range calls {
		value, err := call()
		if err != nil {
			return failed(separateCalls, i+2, start, err)
		}
		parts = append(parts, value)
	}
	return measured(separateCalls, 5, start, parts)
}

func measureOne(name string, ctx context.Context, call func() (map[string]any, error)) measurement {
	start := time.Now()
	value, err := call()
	if err != nil {
		return failed(name, 1, start, err)
	}
	return measured(name, 1, start, value)
}

func measured(name string, calls int, start time.Time, value any) measurement {
	b, _ := json.Marshal(value)
	entry := measurement{Name: name, ToolCalls: calls, ElapsedMS: time.Since(start).Milliseconds(), ResponseBytes: len(b)}
	if parts, ok := value.([]any); ok {
		for _, part := range parts {
			if m, ok := part.(map[string]any); ok && partialResult(m) {
				entry.Partial = true
			}
		}
	} else if m, ok := value.(map[string]any); ok {
		entry.Partial = partialResult(m)
	}
	return entry
}

func partialResult(result map[string]any) bool {
	switch meta := result["meta"].(type) {
	case core.Meta:
		return !meta.Complete || meta.Truncated || meta.SourceTruncated
	case map[string]any:
		complete, _ := meta["complete"].(bool)
		truncated, _ := meta["truncated"].(bool)
		return !complete || truncated
	default:
		return true // unknown completeness must not be called a valid result
	}
}

func failed(name string, calls int, start time.Time, err error) measurement {
	return measurement{Name: name, ToolCalls: calls, ElapsedMS: time.Since(start).Milliseconds(), Error: err.Error()}
}

func copyTarget(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
