# 12 - Complete crash recovery and multi-language integration

Status: ready-for-agent
Blocked by: 09, 10, 11

## Outcome

All supported profiles operate together with correct sharing, partial failure,
timeouts, cancellation, and one-retry crash semantics.

## Work

- Put the single retry budget around complete idempotent tool operations.
- Recreate crashed sessions and resynchronize documents before retry.
- Never retry invalid input, unsupported methods, stale/ambiguous symbols, or
  ordinary LSP response errors.
- Exercise five session keys and six languages; assert TS/JS process sharing.
- Parallelize all-profile search with deterministic aggregation and warnings.
- Add fake LSP modes for startup failure, initialize failure, hang, mid-request
  crash, second crash, malformed response, and graceful exit.
- Add opt-in real-server fixtures for each profile.

## Acceptance

- A first mid-request crash restarts once and can succeed; a second returns
  `LSP_SERVER_CRASHED` with no loop.
- Request deadlines return `REQUEST_TIMEOUT` and cancel the LSP request.
- Missing servers only partially degrade all-language search.
- TS and JS share a process while retaining distinct language IDs.
- Full integration passes under `go test -race`.

## Verification

`go test -race ./...`

