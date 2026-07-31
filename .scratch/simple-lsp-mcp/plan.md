# Simple LSP MCP implementation plan

Status: ready-for-agent

## 1. Delivery strategy

Build the system as a sequence of vertical, testable layers. Stabilize public
contracts and process boundaries first, then add tools in capability groups.
Every ticket must leave `go test ./...` passing and must avoid introducing
semantic fallbacks outside LSP.

The first usable slice ends after issue 08:

```text
MCP initialize/tools/list
  -> search_symbols
  -> get_document_symbols
  -> get_symbol
  -> one lazily started real or fake LSP session
```

The remaining issues add relationship tools, hierarchy tools, diagnostics,
multi-language resilience, and release quality without changing the public
shape established by that slice.

## 2. Package boundaries

```text
cmd/simple-lsp-mcp
  -> internal/mcpserver
    -> internal/tools
      -> internal/{symbol,normalize,document,workspace}
      -> internal/lsp/session
        -> internal/lsp/transport
```

Rules:

- Only `internal/mcpserver` imports the MCP SDK.
- `internal/tools` accepts domain inputs and returns domain outputs/errors; it
  does not construct SDK response objects.
- `internal/lsp/protocol` contains only protocol types used by this product.
- `internal/lsp/transport` knows JSON-RPC framing but no LSP lifecycle.
- `internal/lsp/session` owns process state, capabilities, pending requests,
  server requests, notifications, cancellation, shutdown, and restart.
- `internal/document` owns file snapshots, hashes, versions, didOpen/didChange,
  and position conversion.
- `internal/workspace` is the sole entry point for resolving public paths.
- `internal/symbol` stores ephemeral handles only; it is not an index.

## 3. Key interfaces to stabilize early

Exact names may change during implementation, but responsibility must not leak
across these seams:

```go
type Workspace interface {
    Resolve(relativePath string) (ResolvedPath, error)
}

type Session interface {
    Capabilities() Capabilities
    Request(ctx context.Context, method string, params, result any) error
    Notify(ctx context.Context, method string, params any) error
}

type SessionManager interface {
    ForLanguage(ctx context.Context, language string) (Session, error)
    Shutdown(ctx context.Context) error
}

type Documents interface {
    Sync(ctx context.Context, session Session, path ResolvedPath) (Document, error)
    ToLSPPosition(document Document, public Position, encoding PositionEncoding) (LSPPosition, error)
    ToPublicPosition(document Document, position LSPPosition, encoding PositionEncoding) (Position, error)
}

type Registry interface {
    Register(SymbolRecord) SymbolID
    Resolve(ctx context.Context, id SymbolID) (SymbolRecord, error)
}
```

Tool handlers should share one target-resolution pipeline:

```text
validate target
  -> resolve symbol_id or workspace path
  -> route language/session
  -> sync document
  -> convert position
  -> check capability
  -> request LSP
  -> normalize/jail/truncate
```

## 4. Concurrency and ownership

- One manager entry per session key; TS and JS map to the same key.
- Session startup uses `singleflight`; callers wait on the same initialization.
- Each session allocates monotonically increasing request IDs and owns its
  pending map behind one synchronization strategy.
- One writer goroutine serializes all child stdin frames.
- The reader loop routes responses, server requests, and notifications without
  blocking on slow tool work.
- Cancellation removes the pending request and sends `$/cancelRequest`.
- A tool invocation owns its retry budget. Session internals report a crash;
  they do not recursively retry.
- Multi-language `search_symbols` uses an errgroup and preserves partial results
  as warnings.
- Shutdown is idempotent and bounded by context deadlines.

Run `go test -race ./...` once concurrency is introduced and keep it green.

## 5. Error and truncation policy

Define a typed application error with code, safe message, and optional language
and method. Convert errors to MCP only at the server boundary. Never expose
command arguments, absolute paths, source contents, or stack traces.

Apply limits after normalization and kind/severity filtering. Each list response
reports `complete` and `truncated`; partial cross-language failures are warnings,
not a total failure. A missing requested single-language server remains an
error.

## 6. Test architecture

Use three layers:

1. Pure unit tests for framing, position conversion, path jail, capability
   decoding, normalization, registry rebinding, schemas, and validation.
2. A deterministic fake LSP child process for lifecycle, server requests,
   notifications, cancellation, timeout, crash/retry, and all 13 tool contracts.
3. Opt-in real-server fixtures. Skip only when an executable or advertised
   capability is absent; initialization, routing, sync, and unsupported-method
   behavior remain mandatory where the server is installed.

Test helpers must compare structured MCP output and text JSON semantically,
not byte-for-byte object key order.

## 7. Dependency graph

```text
01 contracts/bootstrap
├─ 02 config/language/workspace
├─ 03 JSON-RPC transport
└─ 07 MCP schemas/server shell

02 + 03 -> 04 session lifecycle
02 + 04 -> 05 document sync/positions
02 + 05 -> 06 normalization/symbol registry
04 + 06 + 07 -> 08 symbol tools (first usable slice)
08 -> 09 definition/reference tools
08 -> 10 call/type hierarchy tools
05 + 08 -> 11 diagnostics
09 + 10 + 11 -> 12 resilience/multi-language integration
12 -> 13 release hardening/documentation
```

Issues 02, 03, and the schema-only portion of 07 may be implemented in
parallel. Tool issues 09, 10, and 11 may be implemented in parallel after 08.

## 8. Milestones and gates

### Gate A: protocol foundation (01-04)

- Fake LSP initializes and shuts down through real stdio framing.
- Concurrent requests, cancellation, server requests, and notifications work
  under `go test -race`.
- No non-MCP output reaches server stdout.

### Gate B: symbol-first vertical slice (05-08)

- `tools/list` exposes exactly 13 tools with valid schemas.
- The chain `search_symbols -> get_symbol -> find_references` is possible, with
  `find_references` completed in issue 09.
- Files are jailed, synchronized, position-safe, and represented by ephemeral
  symbol IDs.

### Gate C: complete feature surface (09-11)

- All relationship, hierarchy, and diagnostics contracts pass against fake LSP.
- Unsupported capabilities return `METHOD_NOT_SUPPORTED`; no alternate search
  is attempted.

### Gate D: production readiness (12-13)

- Five process profiles cover six languages, including TS/JS sharing.
- Crash retry, timeouts, partial search warnings, and shutdown are verified.
- Linux, macOS, and Windows artifacts build in CI and locally through
  GoReleaser snapshot validation.

## 9. Global acceptance criteria

- `go test ./...` and `go test -race ./...` pass.
- `go vet ./...` passes.
- MCP Inspector can initialize, list exactly 13 tools, and invoke every tool
  against fake LSP.
- Public schemas use one-based line/column values and relative paths only.
- Workspace escape cases cover `..`, absolute paths, symlink escape, and Windows
  drive/UNC forms.
- Position tests cover ASCII, Japanese, combining sequences, and emoji in
  UTF-8/UTF-16/UTF-32.
- Source is capped at 64 KiB and result limits honor configured maximums.
- Logs contain method, request ID, duration, and counts but no source bodies or
  full tool arguments.
- No package implements rename, edit, completion, code action, formatting,
  shell execution, text search, or a persistent symbol index.

## 10. Review checkpoints

- After 01: approve public JSON names and error codes before handlers depend on
  them.
- After 04: review goroutine ownership and shutdown/crash state transitions.
- After 06: review stale-symbol rebinding rules with ambiguous fixtures.
- After 08: exercise the first slice manually before multiplying tool handlers.
- After 11: compare every tool and completion condition to design version 0.1.
- Before release: run binaries on all three OS families and at least one real
  fixture for each installed language-server family.

