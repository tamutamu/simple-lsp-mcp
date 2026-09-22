package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/tools"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

type measurement struct {
	Name          string `json:"name"`
	ToolCalls     int    `json:"tool_calls"`
	ElapsedMS     int64  `json:"elapsed_ms"`
	ResponseBytes int    `json:"response_bytes"`
	Error         string `json:"error,omitempty"`
}

func main() {
	var root, symbolPath, path, language string
	var depth, maxBytes int
	var timeout time.Duration
	flag.StringVar(&root, "workspace", ".", "workspace root")
	flag.StringVar(&symbolPath, "symbol", "", "symbol_path to benchmark")
	flag.StringVar(&path, "path", "", "optional workspace-relative source path")
	flag.StringVar(&language, "language", "", "optional language override")
	flag.IntVar(&depth, "depth", 1, "semantic-slice dependency depth")
	flag.IntVar(&maxBytes, "max-bytes", 24576, "semantic-slice maximum serialized JSON bytes")
	flag.DurationVar(&timeout, "timeout", 90*time.Second, "total benchmark timeout")
	flag.Parse()
	if symbolPath == "" {
		fmt.Fprintln(os.Stderr, "--symbol is required")
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

	results := []measurement{
		measureRaw(ctx, engine, target),
		measureOne("get_symbol_context", ctx, func() (map[string]any, error) {
			return engine.SymbolContext(ctx, target)
		}),
		measureOne("get_semantic_slice", ctx, func() (map[string]any, error) {
			in := copyTarget(target)
			in["depth"] = depth
			in["max_bytes"] = maxBytes
			return engine.SemanticSlice(ctx, in)
		}),
	}
	out := map[string]any{
		"target":  target,
		"results": results,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fatal(err)
	}
}

func measureRaw(ctx context.Context, engine *tools.Engine, target map[string]any) measurement {
	start := time.Now()
	parts := make([]any, 0, 5)
	findIn := copyTarget(target)
	findIn["include_source"] = true
	found, err := engine.FindSymbol(ctx, findIn)
	if err != nil {
		return failed("separate_navigation_calls", 1, start, err)
	}
	parts = append(parts, found)
	symbol, _ := found["symbol"].(map[string]any)
	id, _ := symbol["symbol_id"].(string)
	if id == "" {
		return failed("separate_navigation_calls", 1, start, fmt.Errorf("find_symbol did not return one symbol"))
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
			return failed("separate_navigation_calls", i+2, start, err)
		}
		parts = append(parts, value)
	}
	return measured("separate_navigation_calls", 5, start, parts)
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
	return measurement{Name: name, ToolCalls: calls, ElapsedMS: time.Since(start).Milliseconds(), ResponseBytes: len(b)}
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
