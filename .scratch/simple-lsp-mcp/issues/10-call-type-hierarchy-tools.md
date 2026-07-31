# 10 - Implement one-level call and type hierarchy tools

Status: ready-for-agent
Blocked by: 08

## Outcome

Callers, callees, supertypes, and subtypes are returned one level at a time with
reusable symbol handles.

## Work

- Implement prepare-call plus incoming/outgoing requests.
- Implement prepare-type plus supertype/subtype requests.
- Select a prepare item only when uniquely resolvable; return
  `SYMBOL_NOT_FOUND` otherwise.
- Register every returned hierarchy item and preserve its server-supplied data
  needed for the follow-up request.
- Normalize call-site ranges separately from target symbol ranges.
- Do not recursively traverse hierarchy results.

## Acceptance

- All four tools issue prepare followed by exactly one hierarchy request.
- Returned items can be used as targets in subsequent calls.
- Empty/ambiguous prepare results and unsupported capabilities have stable
  errors.
- Tests prove no implicit second-level traversal occurs.

## Verification

`go test ./internal/tools -run 'Incoming|Outgoing|Supertype|Subtype|Hierarchy'`

