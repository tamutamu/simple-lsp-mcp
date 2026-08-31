# 15 - Bound symbol.Registry growth

Status: not started

## Problem Statement

`internal/symbol.Registry` hands out a `symbol_id` for every symbol a tool
touches (`crypto/rand`-based, non-deterministic, no deduplication) and never
removes an entry: no TTL, no LRU, no maximum size. It is scoped to the
process, so a long-running MCP session accumulates entries for as long as it
is open.

The high-level tools added in this pass (`find_symbol`, `get_symbol_outline`,
`get_symbol_context`, `impact_analysis`) register more symbols per call than
the original primitives did — `impact_analysis` alone can register up to
`impactMaxNodes` (200) symbols in a single call — so the rate of growth is
now materially higher than when the registry was first built.

This is a known, accepted tradeoff for this pass, not a regression to fix
here: registering only what a tool actually returns (rather than every
candidate it considered) already keeps each call's contribution bounded, and
the registry's job is to be an ephemeral handle store, not an index.

## Possible directions

- Deduplicate on `(SessionKey, Path, FileHash, SymbolPath)` so the same
  symbol reuses its existing `symbol_id` instead of minting a new one on every
  registration. This would also make `find_symbol`/`get_symbol_context`
  results stable (the same symbol_path always resolving to the same
  symbol_id), which is independently useful to a calling agent.
- Add a maximum entry count with LRU eviction, or a TTL, so a long-lived
  session's memory use is bounded.
- Track process memory in a long-running-session test and decide whether
  either of the above is actually necessary in practice before implementing.

## Non-goals

- Do not turn the registry into a persistent or cross-session index; it must
  stay scoped to one MCP server process.
