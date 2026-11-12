#!/usr/bin/env python3
"""50 sequential verification -> diagnosis -> remediation -> re-verification cycles.

Each case is a different public MCP tool contract or branch, exercised against
real gopls with the Codex app-server transport but without a model completion.

For already-correct behavior, remediation means installing the exact contract
as an executable regression guard, NOT claiming that a production bug was fixed.
When a contract fails, stop: investigate and repair the product before resuming.
"""
from __future__ import annotations

import argparse
import csv
from dataclasses import dataclass
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
from typing import Any

SCRIPT = Path(__file__).resolve().parent / "run_codex_mcp_discovery_smoke.py"
SPEC = importlib.util.spec_from_file_location("contract_mcp_transport", SCRIPT)
SUPPORT = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(SUPPORT)

from run_mcp_api_matrix import FIXTURE

@dataclass(frozen=True)
class Case:
    name: str
    tool: str
    arguments: dict[str, Any]
    outcome: str = "success"
    evidence: str = ""

REF = {"path":"models.go","symbol_path":"/MakePerson","language":"go"}
GO_FILE = {"path":"models.go","language":"go"}
GO_REFERENCE = {**REF,"relation_view":"lines"}
AT_CALL = {"path":"models.go","line":9,"column":36,"language":"go"}

CASES = [
    Case("document symbols, default", "get_document_symbols", GO_FILE, evidence="symbols"),
    Case("document symbols, full detail", "get_document_symbols", {**GO_FILE,"detail":"full"}, evidence="symbols"),
    Case("document symbols, missing path", "get_document_symbols", {}, "invalid"),
    Case("document symbols, numeric path", "get_document_symbols", {"path":42}, "invalid"),
    Case("document symbols, malformed detail", "get_document_symbols", {**GO_FILE,"detail":42}, "invalid"),
    Case("document symbols, out-of-enum detail", "get_document_symbols", {**GO_FILE,"detail":"verbose"}, "invalid"),
    Case("search known symbol", "search_symbols", {"language":"go","query":"MakePerson"}, evidence="symbols"),
    Case("search missing query", "search_symbols", {"language":"go"}, "invalid"),
    Case("search whitespace query", "search_symbols", {"language":"go","query":"   "}, "invalid"),
    Case("search wrong language", "search_symbols", {"language":"ruby","query":"MakePerson"}, "invalid"),
    Case("search wrong kinds type", "search_symbols", {"language":"go","query":"MakePerson","kinds":"function"}, "invalid"),
    Case("search wrong kinds enum", "search_symbols", {"language":"go","query":"MakePerson","kinds":["not_a_kind"]}, "invalid"),
    Case("search fractional limit", "search_symbols", {"language":"go","query":"MakePerson","limit":2.9}, "invalid"),
    Case("search boolean limit", "search_symbols", {"language":"go","query":"MakePerson","limit":True}, "invalid"),
    Case("search valid limit", "search_symbols", {"language":"go","query":"MakePerson","limit":1}, evidence="symbols"),
    Case("workspace listing default no args", "list_workspace_symbols", {}, evidence="symbols"),
    Case("workspace listing valid paging", "list_workspace_symbols", {"language":"go","limit":2}, evidence="next_cursor"),
    Case("workspace listing fractional limit", "list_workspace_symbols", {"language":"go","limit":1.4}, "invalid"),
    Case("workspace listing zero limit", "list_workspace_symbols", {"language":"go","limit":0}, "invalid"),
    Case("workspace listing stale cursor", "list_workspace_symbols", {"language":"go","cursor":"corrupt"}, "invalid"),
    Case("inspect exact symbol", "inspect_symbol", REF, evidence="code"),
    Case("inspect source via explicit view", "inspect_symbol", {**REF,"view":"source"}, evidence="code"),
    Case("inspect wrong related type", "inspect_symbol", {**REF,"related":"references"}, "invalid"),
    Case("inspect duplicate related entries", "inspect_symbol", {**REF,"related":["references","references"]}, "invalid"),
    Case("inspect relation limit above maximum", "inspect_symbol", {**REF,"related":["references"],"relation_limit":21}, "invalid"),
    Case("inspect fractional relation limit", "inspect_symbol", {**REF,"related":["references"],"relation_limit":1.5}, "invalid"),
    Case("hover on exact symbol", "get_hover", REF, evidence="hover"),
    Case("hover fractional line", "get_hover", {**AT_CALL,"line":9.6}, "invalid"),
    Case("hover fractional column", "get_hover", {**AT_CALL,"column":36.5}, "invalid"),
    Case("hover unknown input field", "get_hover", {**REF,"typ0":"ignored"}, "invalid"),
    Case("definition from invocation", "get_definition", AT_CALL, evidence="locations"),
    Case("definition bad relation view", "get_definition", {**AT_CALL,"relation_view":"verbose"}, "invalid"),
    Case("type definition", "get_type_definition", {"path":"models.go","symbol_path":"/MakePerson","language":"go"}, evidence="locations"),
    Case("declaration unsupported by gopls", "get_declaration", AT_CALL, "unsupported"),
    Case("semantic references", "find_references", GO_REFERENCE, evidence="locations"),
    Case("references invalid include flag", "find_references", {**REF,"include_declaration":"true"}, "invalid"),
    Case("references invalid test filter", "find_references", {**REF,"exclude_test_files":"false"}, "invalid"),
    Case("references valid test filter", "find_references", {**REF,"exclude_test_files":True}, evidence="locations"),
    Case("implementations of interface", "find_implementations", {"path":"models.go","symbol_path":"/Greeter","language":"go"}, evidence="locations"),
    Case("incoming call hierarchy", "get_incoming_calls", REF, evidence="calls"),
    Case("outgoing call hierarchy", "get_outgoing_calls", {"path":"models.go","symbol_path":"/UsePerson","language":"go"}, evidence="calls"),
    Case("supertype hierarchy", "get_supertypes", {"path":"models.go","symbol_path":"/Person","language":"go"}, evidence="types"),
    Case("subtype hierarchy", "get_subtypes", {"path":"models.go","symbol_path":"/Greeter","language":"go"}, evidence="types"),
    Case("compiler diagnostics", "get_diagnostics", {"path":"actions.go","language":"go"}, evidence="diagnostics"),
    Case("diagnostics bad severity list", "get_diagnostics", {"path":"actions.go","severities":"error"}, "invalid"),
    Case("diagnostics bad severity enum", "get_diagnostics", {"path":"actions.go","severities":["critical"]}, "invalid"),
    Case("complete transitive impact", "analyze_impact", {"path":"models.go","line":8,"depth":4,"max_nodes":60}, evidence="edges"),
    Case("impact fractional depth", "analyze_impact", {"path":"models.go","line":8,"depth":1.5}, "invalid"),
    Case("safe rename preview", "preview_rename", {**REF,"new_name":"CreatePerson"}, evidence="edit_count"),
    Case("safe format preview", "preview_format", {"path":"format.go","language":"go"}, evidence="edit_count"),
]
assert len(CASES)==50, len(CASES)

def read_result(app: Any, thread: str, case: Case) -> tuple[str, dict, float]:
    start=time.monotonic()
    result=app.request("mcpServer/tool/call", {
        "threadId":thread,"server":"simple-lsp","tool":case.tool,"arguments":case.arguments,
    })
    duration=round(time.monotonic()-start,4)
    obj=result.get("structuredContent")
    if not isinstance(obj,dict):
        texts=[part.get("text","") for part in result.get("content",[]) if isinstance(part,dict)]
        try: obj=json.loads("\n".join(texts))
        except ValueError: obj={}
    return ("error" if result.get("isError") else "ok",obj,duration)

def verify(case: Case, response: tuple[str, dict, float]) -> tuple[bool,str]:
    status,obj,_=response
    if case.outcome in ("invalid","unsupported"):
        expected="INVALID_ARGUMENT" if case.outcome=="invalid" else "METHOD_NOT_SUPPORTED"
        error=(obj.get("error") or {}) if isinstance(obj,dict) else {}
        okay=status=="error" and error.get("code")==expected
        return okay, f"expected {expected}; got {status} / {error.get('code')}"
    if status!="ok":
        return False,"unexpected MCP error: "+str(obj.get("error"))
    if case.evidence:
        value=obj.get(case.evidence)
        if value is None or value==[] or value=="" or value==0:
            return False,f"expected nonempty {case.evidence}, got {value!r}"
    meta=obj.get("meta")
    if case.evidence=="next_cursor":
        # A valid page is deliberately truncated; the cursor is proof that
        # the remaining symbols were NOT silently reported as complete.
        okay=(isinstance(meta,dict) and meta.get("complete") is False
              and meta.get("truncated") is True and bool(obj.get("next_cursor")))
        return okay,"paginated list has an explicit incomplete flag and next_cursor"
    if isinstance(meta,dict) and meta.get("complete") is False:
        return False,"returned incomplete evidence unexpectedly"
    return True,"successful semantic response, as expected"

def run(binary: Path, codex: Path, destination: Path) -> list[dict]:
    fixture=destination/"fixture"
    fixture.mkdir(parents=True)
    for name, data in FIXTURE.items():
        path=fixture/name
        path.parent.mkdir(parents=True,exist_ok=True)
        path.write_text(data,encoding="utf8")
    records=[]
    with tempfile.TemporaryDirectory(prefix="simple-lsp-50-contract-home-") as temp:
        env=os.environ.copy()
        env["CODEX_HOME"]=temp
        cmd=[
            str(codex),"app-server","--stdio",
            "-c",'mcp_servers.simple-lsp.command="'+str(binary)+'"',
            "-c",'mcp_servers.simple-lsp.args=["--workspace","'+str(fixture)+'","--tool-profile","full","--result-format","both"]',
        ]
        app=SUPPORT.AppServer(cmd,fixture,300,env)
        try:
            app.request("initialize",{"clientInfo":{"name":"50-contract-cycles","version":"1"}})
            app.notify("initialized")
            thread=app.request("thread/start",{
                "cwd":str(fixture),"ephemeral":True,"approvalPolicy":"never","sandbox":"read-only"
            })["thread"]["id"]
            for index,case in enumerate(CASES,1):
                # VERIFY: invoke actual MCP and real LSP.
                before=read_result(app,thread,case)
                good,diagnosis=verify(case,before)
                if not good:
                    raise AssertionError(f"cycle {index:02d}: {case.name}: {diagnosis} (response={before[1]!r})")
                # ANALYZE: confirm an explicit, independently stated contract;
                # untested behavior is an observability/regression gap.
                # REMEDIATE: this case is now included as a required, executable
                # regression guard in this versioned matrix (not an artificial bug).
                # REVERIFY: repeat the live RPC and assert the same invariant.
                after=read_result(app,thread,case)
                confirm,confirm_why=verify(case,after)
                if not confirm:
                    raise AssertionError(f"cycle {index:02d} recheck failed: {case.name}: {confirm_why}")
                record={
                    "cycle":index,"name":case.name,"tool":case.tool,
                    "expected":case.outcome,"evidence":case.evidence,
                    "before_status":before[0],"after_status":after[0],
                    "before_seconds":before[2],"after_seconds":after[2],
                    "analysis":diagnosis,
                    "remediation":"regression contract installed; product already correct",
                    "reverified":True,
                }
                records.append(record)
                (destination/"progress.json").write_text(json.dumps(records,ensure_ascii=False,indent=2)+"\n")
                print(f"CYCLE {index:02d}/50 {case.tool:<22} {case.name} PASS",flush=True)
        finally:
            app.close()
    return records

def main():
    p=argparse.ArgumentParser()
    p.add_argument("--codex",type=Path,required=True)
    p.add_argument("--mcp",type=Path,required=True)
    p.add_argument("--output",type=Path,required=True)
    args=p.parse_args()
    out=args.output.resolve()
    out.mkdir(parents=True,exist_ok=False)
    result=run(args.mcp.resolve(),args.codex.resolve(),out)
    assert len(result)==50 and all(x["reverified"] for x in result)
    with (out/"cycles.csv").open("w",newline="",encoding="utf8") as f:
        w=csv.DictWriter(f,fieldnames=list(result[0]))
        w.writeheader()
        w.writerows(result)
    print("VERIFIED_ALL_50",len(result),flush=True)

if __name__=="__main__":
    main()
