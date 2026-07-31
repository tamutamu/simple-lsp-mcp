# 04 - Implement LSP session lifecycle and capability handling

Status: ready-for-agent
Blocked by: 02, 03

## Outcome

Language-server child processes initialize lazily, expose normalized
capabilities, handle server-initiated traffic, and shut down predictably.

## Work

- Implement the Stopped/Starting/Initializing/Ready/Stopping/Failed state
  machine with singleflight startup.
- Launch command arrays directly and initialize with workspace folder,
  `clientInfo`, position encodings, and required client capabilities.
- Decode boolean and options-object provider capabilities.
- Send `initialized`; implement bounded `shutdown` then `exit`.
- Handle configuration, workspace folders, dynamic registration state, progress,
  log/show messages, diagnostics, and read-only `workspace/applyEdit`.
- Make shutdown idempotent and complete pending calls on unexpected exit.
- Preserve the negotiated encoding, defaulting to UTF-16 when absent.

## Acceptance

- Many simultaneous first requests start one process.
- Capability checks distinguish absent, false, true, and options-object values.
- `workspace/applyEdit` returns `applied=false` with the required reason.
- Crash and shutdown unblock every pending request with stable errors.
- Fake LSP integration passes under the race detector.

## Verification

`go test -race ./internal/lsp/session ./internal/lsp`

