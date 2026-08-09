package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/mcpserver"
	"github.com/tamutamu/simple-lsp-mcp/internal/tools"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

var version = "dev"

func main() {
	var root, logLevel string
	var timeout, wait time.Duration
	var max int

	// Parse server settings before starting the stdio transport.
	flag.StringVar(&root, "workspace", "", "workspace root (required)")
	flag.StringVar(&logLevel, "log-level", "info", "debug|info|warn|error")
	flag.DurationVar(&timeout, "request-timeout", 15*time.Second, "LSP request timeout")
	flag.DurationVar(&wait, "diagnostics-wait", 2*time.Second, "push diagnostics wait")
	flag.IntVar(&max, "max-results", 500, "maximum result count")
	flag.Parse()
	if root == "" {
		fmt.Fprintln(os.Stderr, "--workspace is required")
		os.Exit(2)
	}

	level := new(slog.LevelVar)
	if err := level.UnmarshalText([]byte(logLevel)); err != nil {
		fmt.Fprintln(os.Stderr, "invalid --log-level")
		os.Exit(2)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	ws, err := workspace.Open(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	cfg, err := config.Load(config.Runtime{Workspace: ws.Root(), RequestTimeout: timeout, DiagnosticsWait: wait, MaxResults: max})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	engine := tools.New(ws, cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := mcpserver.New(engine, version)
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		slog.Error("mcp server stopped", "error", err)
	}

	// Let active language servers exit cleanly before forcing termination.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	engine.Sessions.Shutdown(shutdownCtx)
}
