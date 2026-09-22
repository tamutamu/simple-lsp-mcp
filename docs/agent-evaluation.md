# Agent coding-task evaluation protocol

**Status:** reproducible protocol; no agent-success measurements have been collected by this document. Do not infer effectiveness from tool-call or byte counts alone.

1. Select and pin public Go, TypeScript, and Python repository commits and issue-style tasks with objective acceptance tests. Record repository commit SHA, model identifier, agent version, LSP version, tool version and exact prompt. Do not select only tasks known to favor this MCP.
2. Run the same tasks in clean independent checkouts with identical budgets, model, permissions, network settings and prompts. A: agent with its normal tools; B: same agent plus `simple-lsp-mcp`. Run multiple repetitions with randomized A/B ordering and separately track failed or timed-out trials.
3. Evaluate in a fresh checkout with tests not supplied to the agent when possible. Record test success, behavior correctness, regressions, incomplete searches, tool calls, elapsed wall time and actual model usage reported by the agent (if available); do not use JSON bytes as a fabricated token count.
4. Report all tasks, including those where the MCP is slower or less accurate, with the evaluation scripts, raw redacted logs and confidence intervals. Inspect logs for whether the agent actually called the MCP before attributing any change to it.
5. Obtain consent before using private code in any evaluation and strip paths, secrets, and source content before publishing logs.

The included `go run ./cmd/simple-lsp-bench` command is a **microbenchmark** only. A task-level evaluation requires running actual agent sessions; it is not performed automatically by CI and cannot be claimed as a benchmark result until executed.

## Runnable opt-in paired evaluation

The repository includes `scripts/evaluate-agent-tasks.py` and a small intentionally
broken Go fixture in `testdata/evaluation/`. The harness **never invokes agent
commands without `--execute`**. All commands are JSON argv arrays, executed
without a shell. It copies each task into a fresh directory, randomly varies
baseline/MCP ordering (reproducible seed), and runs acceptance tests that are
injected **after** the agent finishes so editing visible tests alone cannot pass.

```sh
python3 scripts/evaluate-agent-tasks.py \
  --manifest testdata/evaluation/tasks.json \
  --baseline '["codex", "exec", "--ephemeral", "--skip-git-repo-check", "{prompt}"]' \
  --mcp '["codex", "exec", "--ephemeral", "--skip-git-repo-check", "{prompt}"]'
```

The command above is **preview only**, and the example deliberately does not
configure an MCP server: identical baseline/MCP commands are not a valid efficacy
comparison. Before adding `--execute`, supply an MCP-enabled command using
Codex/Claude CLI configuration (the `{server}` and `{workspace}` placeholders
expand to the server executable and each fresh task directory). Make sure both
variants use the same model, permissions and budgets. The harness does not
configure or validate your agent; review its command lines before execution.
Agents may consume paid API quota, invoke tools and access your network. Run
only in an environment you trust. The test command in the manifest is also
executed deliberately and may run arbitrary project code.

```sh
python3 scripts/evaluate-agent-tasks.py \
  --manifest testdata/evaluation/tasks.json \
  --baseline '<JSON argv for baseline agent>' \
  --mcp '<JSON argv for same agent with MCP enabled>' \
  --server /absolute/path/to/simple-lsp-mcp \
  --repeats 3 --seed 42 --execute --output results.json
```

`--keep-logs DIR` retains agent logs **and modified source code** for auditing;
without it, trial files and logs are deleted when the command returns. The
summary contains only task IDs, exit statuses, elapsed time, and acceptance-test
outcomes. It does not claim the agent used the MCP: inspect retained logs and
verify actual tool calls before attributing differences to `simple-lsp-mcp`.
It also does not measure billing tokens or guarantee causal effects. Record
agent/model versions, commit SHA, and fixture revisions alongside the report.
The included fixture is a harness smoke test, not evidence of any real agent gain.

To exercise the harness and hidden-oracle checks **without** running any LLM:

```sh
python3 -m unittest discover -s scripts -p 'test_*.py'
```
