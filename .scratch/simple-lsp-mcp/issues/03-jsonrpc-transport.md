# 03 - Implement concurrent stdio JSON-RPC transport

Status: ready-for-agent
Blocked by: 01

## Outcome

A lifecycle-neutral transport safely frames, sends, receives, cancels, and
dispatches JSON-RPC messages over child stdio.

## Work

- Parse mandatory case-insensitive `Content-Length` headers with bounded sizes.
- Support split reads, multiple frames per read, notifications, requests,
  responses, JSON-RPC errors, and out-of-order responses.
- Allocate uint64 request IDs and maintain a concurrency-safe pending map.
- Serialize writes through one writer goroutine.
- Send `$/cancelRequest` on context cancellation and release pending entries.
- Expose callbacks/channels for server requests and notifications.
- Forward child stderr through an injected logger, never stdout.
- Build a deterministic fake transport peer for malformed frames and races.

## Acceptance

- Concurrent requests receive the correct out-of-order responses.
- Cancellation, EOF, malformed headers, invalid JSON, and writer failure unblock
  all affected callers exactly once.
- Frame and payload limits prevent unbounded allocation.
- `go test -race` finds no transport races or goroutine leaks.

## Verification

`go test -race ./internal/lsp/transport`

