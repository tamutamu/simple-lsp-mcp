# Semantic Change Guard value demo

This fixture is intentionally small. Its purpose is to make the value of `verify_change` visible in a case where ordinary compilation and the existing test suite are both green.

`CompleteOrder` has two observable responsibilities:

1. return the completed order result, and
2. call `Audit`.

The test intentionally checks only the first responsibility. That gap creates a realistic silent-regression scenario: deleting the audit call still compiles and still passes the test.

## Prerequisites

Install `gopls` and use a `simple-lsp-mcp` binary built from this checkout. For a clean demonstration, use a temporary worktree so unrelated local changes do not appear in `verify_change`:

```sh
DEMO=/tmp/simple-lsp-value-demo
rm -rf "$DEMO"
git worktree add --detach "$DEMO" HEAD
cd "$DEMO"

go test ./examples/change-guard-demo
simple-lsp-mcp doctor --workspace . --probe examples/change-guard-demo/process.go
```

Expected result: the Go test passes and `doctor` successfully probes `process.go` with gopls.

When using Claude Code, Codex, or another MCP client, launch the client from `$DEMO` or configure `simple-lsp-mcp --workspace "$DEMO"`.

## Demo 1 — silent semantic regression that tests miss

First confirm the baseline is green:

```sh
go test ./examples/change-guard-demo
```

Expected:

```text
ok .../examples/change-guard-demo
```

Now delete only the audit call:

```sh
python3 - <<'PY'
from pathlib import Path
p = Path("examples/change-guard-demo/process.go")
s = p.read_text()
p.write_text(s.replace("\tAudit(orderID)\n", ""))
PY
```

Run the normal test again:

```sh
go test ./examples/change-guard-demo
```

**Expected: it still passes.** There is no type error and the test suite does not cover the missing side effect.

Now ask the connected MCP client to call:

```text
verify_change(base="HEAD")
```

A useful agent prompt is:

```text
Use simple-lsp's verify_change against HEAD. Show the verdict, new diagnostics,
and any removed semantic relations. Do not infer the answer from grep.
```

Expected result contains the equivalent of:

```text
verdict: WARNING
new diagnostics: 0
semantic_diff.removed_count: >= 1

finding:
  code: SEMANTIC_RELATION_REMOVED
  symbol: CompleteOrder
  path: examples/change-guard-demo/audit.go
  message: outgoing_call relation disappeared (1 -> 0) while the related file was not changed
```

### What this demonstrates

`go test` says the change is fine. The compiler says the change is fine. A text search can tell you that `Audit` exists, but it does not independently tell you that the committed `CompleteOrder -> Audit` semantic relationship disappeared.

`verify_change` compares the language-server graph for the committed base with the current graph and surfaces the lost relation as a review warning. It does **not** claim that removing the call is always wrong; it tells the coding agent, "this meaningful relationship disappeared and nothing else explains it — review it before declaring the task complete."

That is the core value of the Change Guard: catching plausible omissions that are semantically visible but not compiler errors.

Restore the file before the next demo:

```sh
git restore examples/change-guard-demo/process.go
```

## Demo 2 — detect, repair, re-verify

Introduce a fixable compile diagnostic by adding an unused import:

```sh
python3 - <<'PY'
from pathlib import Path
p = Path("examples/change-guard-demo/process.go")
s = p.read_text()
p.write_text(s.replace("package changeguarddemo\n", 'package changeguarddemo\n\nimport "fmt"\n'))
PY
```

Ask the MCP client to call:

```text
verify_change(base="HEAD")
```

Expected:

```text
verdict: FAIL
diagnostic_diff.new: contains the unused-import diagnostic
repair_candidates: contains a gopls WorkspaceEdit-backed action
```

Each usable repair candidate includes `apply_input`. Pass that object directly to:

```text
apply_code_action(...apply_input...)
```

Then call:

```text
verify_change(base="HEAD")
```

Expected final result:

```text
verdict: PASS
findings: []
new diagnostics: []
```

### What this demonstrates

The agent does not need to invent a textual patch for an LSP-known problem. The flow is:

```text
agent edit
  -> verify_change
  -> proven new diagnostic
  -> language-server repair candidate
  -> apply_code_action
  -> verify_change again
  -> PASS
```

The language server remains the semantic authority, while the MCP turns its primitives into a development-completion loop.

## The value in one sentence

Without `simple-lsp-mcp`, a coding agent mainly asks **"does the code compile and do tests pass?"** With the Change Guard it can also ask **"what semantic relationships did my change silently remove, and did I introduce a fixable language-server regression?"** before saying the work is complete.

## Cleanup

From the original checkout:

```sh
git worktree remove /tmp/simple-lsp-value-demo --force
```
