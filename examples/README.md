# Examples

Examples in this directory are executable proof fixtures, not screenshots or hand-written demos. CI should exercise the same files with real language servers so the claims do not silently rot.

- [`react-fastapi-monorepo`](react-fastapi-monorepo/) — one MCP process, TypeScript/React + Python/FastAPI, per-project LSP configuration, cross-file semantic navigation, and mixed-language workspace enumeration.

- [`change-guard-demo`](change-guard-demo/) — a silent semantic-regression demo where tests stay green but `verify_change` detects a removed call relation, plus a detect/repair/re-verify loop.
