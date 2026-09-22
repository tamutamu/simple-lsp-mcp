# Agent coding-task evaluation protocol

**Status:** reproducible protocol; no agent-success measurements have been collected by this document. Do not infer effectiveness from tool-call or byte counts alone.

1. Select and pin public Go, TypeScript, and Python repository commits and issue-style tasks with objective acceptance tests. Record repository commit SHA, model identifier, agent version, LSP version, tool version and exact prompt. Do not select only tasks known to favor this MCP.
2. Run the same tasks in clean independent checkouts with identical budgets, model, permissions, network settings and prompts. A: agent with its normal tools; B: same agent plus `simple-lsp-mcp`. Run multiple repetitions with randomized A/B ordering and separately track failed or timed-out trials.
3. Evaluate in a fresh checkout with tests not supplied to the agent when possible. Record test success, behavior correctness, regressions, incomplete searches, tool calls, elapsed wall time and actual model usage reported by the agent (if available); do not use JSON bytes as a fabricated token count.
4. Report all tasks, including those where the MCP is slower or less accurate, with the evaluation scripts, raw redacted logs and confidence intervals. Inspect logs for whether the agent actually called the MCP before attributing any change to it.
5. Obtain consent before using private code in any evaluation and strip paths, secrets, and source content before publishing logs.

The included `go run ./cmd/simple-lsp-bench` command is a **microbenchmark** only. A task-level evaluation requires running actual agent sessions; it is not performed automatically by CI and cannot be claimed as a benchmark result until executed.
