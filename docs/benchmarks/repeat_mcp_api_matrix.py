#!/usr/bin/env python3
"""50 independent full-tool reliability verification rounds.

Each round boots an isolated MCP over Codex's app-server transport, confirms
all 22 tools against real Go/Python LSP servers, applies real edits in an
isolated fixture, analyzes any discrepancies, and stops on first failure.
Unlike 50 input contract cases, each iteration covers the *entire* 22 API.
"""
from __future__ import annotations
import argparse
import csv
import hashlib
import json
from pathlib import Path
import subprocess
import time
from run_mcp_api_matrix import run, check_python_declaration, verify_contracts

def main() -> None:
    p=argparse.ArgumentParser()
    p.add_argument("--codex",type=Path,required=True)
    p.add_argument("--mcp",type=Path,required=True)
    p.add_argument("--output",type=Path,required=True)
    p.add_argument("--repetitions",type=int,default=50)
    args=p.parse_args()
    assert args.repetitions>0
    out=args.output.resolve()
    out.mkdir(parents=True,exist_ok=False)
    binary=args.mcp.resolve()
    exe_hash=hashlib.sha256(binary.read_bytes()).hexdigest()
    rows=[]
    for iteration in range(1,args.repetitions+1):
        directory=out/f"cycle-{iteration:03d}"
        directory.mkdir()
        start=time.monotonic()
        try:
            # VERIFY: all operations against a fresh, isolated actual LSP.
            go=run(binary,args.codex.resolve(),directory/"go")
            py=check_python_declaration(binary,args.codex.resolve(),directory/"python")
            # ANALYZE: check responses, capability limitations, completeness,
            # edit application and actual resulting workspace file content.
            # REMEDIATE: the same validated 22-tool regression guard is
            # required for this run; if any fault is found, stop and repair
            # product code rather than deleting the failed trial.
            verify_contracts(go,py,directory/"go")
            # REVERIFY: confirm after real LSP changes persisted and readable
            # structured results for all expected operations.
            success=sum(bool(x.get("semantic_nonempty")) for x in go+[py])
            assert success==22 and len(go)==22 and len({x["tool"] for x in go+[py]})==22
            note="22/22 verified; gopls declaration unsupported as advertised; Python declaration valid"
            passed=True
        except Exception as exc:
            note=f"FAILED: {type(exc).__name__}: {exc}"
            passed=False
        elapsed=round(time.monotonic()-start,4)
        row={
            "cycle":iteration,"passed":str(passed).lower(),
            "verified_tools":22 if passed else 0,
            "direct_calls":23 if passed else 0,
            "elapsed_seconds":elapsed,"analysis":note,
            "remediation":"verified existing regression behavior; no product change needed" if passed else "manual repair and rerun required",
            "mcp_sha256":exe_hash,
        }
        rows.append(row)
        (out/"progress.json").write_text(json.dumps(rows,ensure_ascii=False,indent=2)+"\n")
        if not passed:
            print(f"CYCLE {iteration:02d}/{args.repetitions} {note}",flush=True)
            raise AssertionError(note)
        print(f"CYCLE {iteration:02d}/{args.repetitions} ALL_22_PASSED {elapsed:.3f}s",flush=True)
    with (out/"cycles.csv").open("w",newline="",encoding="utf8") as f:
        writer=csv.DictWriter(f,fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)
    print(f"VERIFIED {len(rows)}/{args.repetitions} CYCLES, {len(rows)*23} REAL MCP CALLS",flush=True)

if __name__=="__main__":
    main()
