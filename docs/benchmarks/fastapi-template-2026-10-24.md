# Coding-agent A/B evaluation: official FastAPI full-stack template

Evaluation date: **2026-10-24**. Source: [fastapi/full-stack-fastapi-template](https://github.com/fastapi/full-stack-fastapi-template) at pinned commit [`b1125d883948`](https://github.com/fastapi/full-stack-fastapi-template/commit/b1125d8839482cb7bbae603ddc6d0081bf87532e). Tool under test: locally built `simple-lsp-mcp` from v0.10.2 source.

**Question:** Does enabling simple-lsp-mcp improve actual coding-agent results compared with the same agent without any MCP?

## Results

Six tasks, **three independent runs per task and condition**: 36 separate agent sessions. Figures are **arithmetic means** of three runs. All successes below were established by a grader outside the agent session, not by accepting the agent's final answer.

| Task | Acceptance without / with MCP | Time (s), without → with | API cost (USD), without → with | Tool calls, without → with |
| --- | --- | ---: | ---: | ---: |
| Search feature: item title/description, tenant-scoped | 3/3 · 3/3 | 23.37 → 23.03 | 0.0802 → 0.0992 | 5.33 → 8.00 |
| Paging bug: validation and stable sorting | 3/3 · 3/3 | 14.23 → 22.23 | 0.0495 → 0.0921 | 4.00 → 9.67 |
| Authorization refactor: share item lookup/guard | 3/3 · 3/3 | 15.93 → 19.57 | 0.0559 → 0.0790 | 7.00 → 10.00 |
| Summary feature: owner-scoped aggregate API | 3/3 · 3/3 | 25.10 → 34.10 | 0.0755 → 0.1252 | 6.00 → 11.67 |
| Rename: Python `get_user_by_email` across app/tests | 3/3 · 3/3 | 17.03 → 19.53 | 0.0396 → 0.0919 | 3.00 → 7.00 |
| Rename: Python `get_password_hash` across app/tests | 3/3 · 3/3 | 11.73 → 17.90 | 0.0351 → 0.0793 | 2.67 → 7.00 |
| **All tasks, equal task weighting** | **18/18 · 18/18** | **17.90 → 22.73** | **0.0560 → 0.0945** | **4.67 → 8.89** |

Across these 36 runs, MCP-available agents actually invoked simple-lsp **47 times**, while agents in **both** conditions invoked Claude's own LSP **zero times**. MCP was used in all 18 treatment sessions.

**Interpretation:** All 36 solutions passed the specified acceptance checks. MCP did not improve observed task success. In these relatively small, localized FastAPI template changes, having MCP available raised mean elapsed time **27%** and reported API cost **69%**; five of the six scenarios were slower with MCP. The search feature's difference in elapsed time was negligible. We **must not** advertise a general productivity, correctness, or cost improvement based on this experiment. This is a negative but useful baseline for future product work.

## How we controlled the comparison

- **Code:** each run used a fresh independent checkout of exactly the same upstream commit and started from a clean Git baseline. No changes were submitted to the upstream repository.
- **Agent:** Claude Code 2.1.301; `claude-sonnet-5-5` (`--model sonnet`); medium effort; up to 24 turns; per-run maximum quoted API cost $0.85; fresh process and `--no-session-persistence`.
- **Tool controls:** both conditions had the same ordinary Read, Edit, Write, Glob, Grep, Bash and ToolSearch permissions; `--strict-mcp-config`; `--disallowedTools LSP`; and `--disable-slash-commands`. The *sole intended difference* was registering one simple-lsp-mcp server in the treatment condition. No Claude LSP plugin or native Claude LSP access was involved.
- **Instructions:** both conditions received an identical `CLAUDE.md`: when simple-lsp is connected, inspect relevant code semantically and verify the change; otherwise use ordinary tools. Upstream `.claude` and `.agents` content was excluded from **both** checkouts. The same user task prompt (see [task definitions](fastapi-template-tasks.json)) was used for both conditions, followed by this common suffix: “Work only on this official FastAPI template checkout. No PostgreSQL is running. Do not add dependencies or edit generated clients. Run syntax checks and write tests if useful.”
- **Execution:** trials ran on one Linux workstation, at most four in parallel. Order was alternated where possible (first and third repeats generally started baseline first; second repeat MCP first). Cold agent/MCP process startup, server startup, cache and host load are included in wall time.
- **Measurements:** elapsed time is wall time for the entire CLI process; cost is Claude Code's own reported `total_cost_usd` (an estimate, **not** a billed receipt); tool calls are agent-visible tool uses recorded from stream-json traces, including MCP calls and discovery.
- **Result archives:** [all 36 per-run records](fastapi-template-2026-10-24.csv) list success, times, costs, tool calls, changed files and grader database. Original full conversation traces remain outside the Git repository to avoid committing potentially sensitive model/session data.

## Independent acceptance checks

The original, unchanged upstream checkout was verified to **fail** each task-specific acceptance check. Graders were not supplied to the coding agents.

- **Search:** real FastAPI TestClient requests, SQLModel database records, mixed-case title and description matching, blank queries, count before pagination, two owners and a superuser.
- **Paging:** HTTP 422 for invalid skip/limit on users **and** items, proper user endpoint authentication, stable ordering when timestamps tie, valid pagination. This task checks only `created_at DESC, id DESC` for item ordering as requested.
- **Authorization:** AST confirms a shared helper is called from all three endpoint functions; live HTTP tests check 200/403/404 for owners, other users and superusers.
- **Summary:** **disposable PostgreSQL 16** was used for SQL aggregation validation (including PostgreSQL-specific `btrim`/regex), as well as owner/admin counts and OpenAPI registration. PostgreSQL is removed after grading.
- **Renames:** Python AST checks every declaration, import and name/attribute reference across `backend/app` and `backend/tests`; runtime CRUD lookup and password hash/verify round-trip are exercised.
- **All tasks:** independent Python compileall check and `git diff --check`.

The [standalone acceptance grader](fastapi-template-grader.py) uses Python 3.14 and the official template's pinned dependencies. It is intended for **disposable checkout copies** only. PostgreSQL grading intentionally guards against non-local databases and requires an explicitly disposable local database named `bench` plus `BENCH_ALLOW_DISPOSABLE_RESET=YES`; the grader resets that database schema.

### Corrections to the test harness

The initial paging grader accidentally requested the privileged `/users/` endpoint as an ordinary user, causing a misleading HTTP 403 versus expected HTTP 422. The test was corrected to use the admin account, and **all six unmodified agent outputs** were regraded successfully. The initial summary grader used SQLite, which cannot execute PostgreSQL-specific regex/`btrim` SQL generated by otherwise valid solutions. **All six unmodified outputs** were then regraded against actual PostgreSQL 16 and passed. These are **harness corrections, not repairs to agent-written code**. The original raw evaluations are not represented as valid quality failures in the final dataset.

## Boundaries and product implications

These tasks exercise genuine source edits, behavior and access control, but the official template is a manageable-sized project; baseline agents can solve many of these tasks with only a few well-targeted file and shell operations. The acceptance checks do not exhaustively prove semantic equivalence, security, maintainability or production deployment quality. Three repeats are useful for direction, **not** for a statistical generalization. Separate process initialization and warm/cold caches may change timing.

This result does not contradict the [earlier three-pair Go symbol-rename pilot](../evidence.md#coding-agent-outcome-ab-pilot-2026-10-24), which showed a reduction in tool calls on a different repository and task. **The benefit is workload-dependent.**

Evidence-supported simple-lsp capabilities remain semantic LSP identity, reference navigation, caller/impact traversal, safe preview/apply renames, and post-edit diagnostics. Those are useful features, but in **this** measured workload, invoking them did not make Claude Code more effective. The right follow-up is to reduce needless semantic-tool calls/latency and test larger, ambiguous, multi-module tasks where text-based exploration truly has a higher failure rate—not to conceal these negative results or add benchmark-only MCP features.
