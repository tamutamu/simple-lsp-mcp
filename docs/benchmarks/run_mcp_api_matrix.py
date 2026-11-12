#!/usr/bin/env python3
"""Direct product API matrix, not an agent productivity experiment.

Invoke 22 advertised MCP tools against real gopls in an isolated sample,
retain exact arguments and status; don't count empty/failed results as proof.
"""
from __future__ import annotations
import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import tempfile
import time

SCRIPT = Path(__file__).resolve().parent / "run_codex_mcp_discovery_smoke.py"
SPEC = importlib.util.spec_from_file_location("mcp_probe_support", SCRIPT)
SUPPORT = importlib.util.module_from_spec(SPEC)
assert SPEC.loader
SPEC.loader.exec_module(SUPPORT)

FIXTURE = {
    "go.mod": "module example.com/matrix22\n\ngo 1.26.0\n",
    "models.go": """package matrix22

type Greeter interface {
    Greet() string
}
type Person struct { Name string }
func (p Person) Greet() string { return "hi " + p.Name }
func MakePerson(name string) Person { return Person{Name: name} }
func UsePerson() string { return MakePerson("alice").Greet() }
""",
    "caller.go": """package matrix22
func CallPerson() string {
    return UsePerson()
}
""",
    "format.go": """package matrix22
func NeedsFormat( )int{return 42}
""",
    "actions.go": """package matrix22
import (
   "fmt"
)
func UnusedAction() int { return 1 }
""",
}

def run(binary: Path, codex: Path, destination: Path) -> list[dict]:
    destination.mkdir(parents=True, exist_ok=True)
    for path, code in FIXTURE.items():
        f = destination / path
        f.parent.mkdir(parents=True, exist_ok=True)
        f.write_text(code)
    all_tools = set()
    records = []
    with tempfile.TemporaryDirectory(prefix="simple-lsp-api-matrix-home-") as temporary:
        env = os.environ.copy()
        env["CODEX_HOME"] = temporary
        args = [
            str(codex), "app-server", "--stdio",
            "-c", 'mcp_servers.simple-lsp.command="' + str(binary) + '"',
            "-c", 'mcp_servers.simple-lsp.args=["--workspace","' + str(destination) + '","--tool-profile","full","--result-format","both"]',
        ]
        app = SUPPORT.AppServer(args, destination, 450, env)
        # A write is authorized only inside the generated temporary fixture.
        # Codex may relay a server-side edit approval as an RPC request.
        original_read = app._read
        def approved_fixture_only():
            while True:
                packet = original_read()
                params = packet.get("params") or {}
                if packet.get("method") == "mcpServer/elicitation/request" and "id" in packet:
                    meta = params.get("_meta") or {}
                    target = (meta.get("tool_params") or {}).get("path", "")
                    inside = (
                        params.get("serverName") == "simple-lsp"
                        and meta.get("codex_approval_kind") == "mcp_tool_call"
                        and (not target or (not Path(target).is_absolute() and ".." not in Path(target).parts))
                    )
                    app._write({
                        "jsonrpc":"2.0","id":packet["id"],
                        "result":{"action":"accept","content":{}} if inside else {"action":"decline"}
                    })
                    continue
                if packet.get("method","").endswith("requestApproval") and "id" in packet:
                    app._write({"jsonrpc":"2.0","id":packet["id"],"result":{"decision":"decline"}})
                    continue
                return packet
        app._read = approved_fixture_only
        try:
            app.request("initialize", {"clientInfo": {"name":"simple-lsp-matrix","version":"1"}})
            app.notify("initialized")
            thread = app.request("thread/start", {
                "cwd":str(destination),"ephemeral":True,"approvalPolicy":"never","sandbox":"workspace-write"
            })["thread"]["id"]
            inventory = app.request("mcpServerStatus/list", {"threadId":thread, "detail":"full"})
            for item in inventory["data"]:
                if item["name"]=="simple-lsp":
                    all_tools = set(item["tools"])
                    break
            if len(all_tools) != 22:
                raise ValueError(f"Expected 22 advertised tools; got {sorted(all_tools)}")
            def call(tool: str, params: dict) -> dict:
                start=time.monotonic()
                try:
                    result=app.request("mcpServer/tool/call",{
                        "threadId":thread,"server":"simple-lsp",
                        "tool":tool,"arguments":params
                    })
                    text="\n".join(str(x.get("text","")) for x in result.get("content",[]))
                    is_error=bool(result.get("isError"))
                    parsed=result.get("structuredContent")
                    if parsed is None:
                        try: parsed=json.loads(text)
                        except json.JSONDecodeError: parsed=None
                    semantic=bool(parsed) and (not is_error)
                    if isinstance(parsed,dict) and parsed.get("meta",{}).get("complete") is False:
                        semantic=False
                    if isinstance(parsed,dict) and parsed.get("locations")==[]:
                        semantic=False
                    if isinstance(parsed,dict) and parsed.get("actions")==[]:
                        semantic=False
                    row={
                        "tool":tool,"language":"go","arguments":params,"isError":is_error,
                        "result_bytes":len(text.encode()),
                        "elapsed_sec":round(time.monotonic()-start,3),
                        "semantic_nonempty":semantic,
                        "preview":text[:900]
                    }
                    if isinstance(parsed,dict):
                        row["keys"]=sorted(parsed)
                        if "locations" in parsed: row["locations"]=len(parsed["locations"])
                        if "actions" in parsed: row["actions"]=len(parsed["actions"])
                        if "meta" in parsed: row["meta"]=parsed["meta"]
                        if "error" in parsed: row["error"]=parsed["error"]
                    records.append(row)
                    print(tool, "error",is_error,"bytes",len(text.encode()),"sec",row["elapsed_sec"],"semantic",semantic,flush=True)
                    return row
                except Exception as exc:
                    row={"tool":tool,"language":"go","arguments":params,"exception":str(exc),"elapsed_sec":round(time.monotonic()-start,3)}
                    records.append(row)
                    print(tool,"EXCEPTION",str(exc)[:190],flush=True)
                    return row
            base={"path":"models.go","line":8,"column":6,"language":"go"}
            target={"path":"models.go","symbol_path":"/MakePerson","language":"go"}
            call("get_document_symbols", {"path":"models.go","language":"go"})
            call("search_symbols", {"query":"MakePerson","language":"go"})
            call("list_workspace_symbols", {"language":"go","limit":40})
            call("inspect_symbol",{**target,"view":"edit"})
            call("get_hover",target)
            call("get_definition",{"path":"models.go","line":9,"column":36,"language":"go","relation_view":"lines"})
            call("get_declaration",{"path":"models.go","line":9,"column":36,"language":"go"})
            call("get_type_definition",{"path":"models.go","line":9,"column":36,"language":"go"})
            call("find_references",{**target,"include_declaration":False,"relation_view":"lines"})
            call("find_implementations",{"path":"models.go","symbol_path":"/Greeter","language":"go"})
            call("get_incoming_calls",target)
            call("get_outgoing_calls",{"path":"models.go","symbol_path":"/UsePerson","language":"go"})
            call("get_supertypes",{"path":"models.go","symbol_path":"/Person","language":"go"})
            call("get_subtypes",{"path":"models.go","symbol_path":"/Greeter","language":"go"})
            call("get_diagnostics",{"path":"actions.go","language":"go"})
            call("analyze_impact",{"path":"models.go","line":8,"depth":4,"max_nodes":60})
            call("preview_format",{"path":"format.go","language":"go"})
            call("format_document",{"path":"format.go","language":"go","apply":True})
            actions=call("get_code_actions",{"path":"actions.go","line":2,"column":1,"language":"go"})
            # The action id is not a guess: only apply one returned by the server.
            txt=actions.get("preview","")
            try: data=json.loads(txt)
            except Exception: data={}
            action_list=data.get("actions",[]) if isinstance(data,dict) else []
            if action_list:
                action_id=next((a.get("action_id") for a in action_list if a.get("has_edit") and not a.get("disabled") and not a.get("has_command")),None)
                if action_id:
                    call("apply_code_action",{"path":"actions.go","line":2,"column":1,"action_id":action_id,"language":"go"})
            if not any(x["tool"]=="apply_code_action" for x in records):
                call("apply_code_action",{"path":"actions.go","line":2,"column":1,"action_id":"invalid-id","language":"go"})
            call("preview_rename",{**target,"new_name":"CreatePerson"})
            call("rename_symbol",{**target,"new_name":"CreatePerson","apply":True})
        finally: app.close()
    coverage = {x["tool"] for x in records}
    if coverage != all_tools:
        raise ValueError(f"Calls cover {len(coverage)}/{len(all_tools)}; missing {sorted(all_tools-coverage)}")
    return records


def check_python_declaration(binary: Path, codex: Path, destination: Path) -> dict:
    """gopls doesn't advertise declaration; Pyright exposes it for Python."""
    destination.mkdir(parents=True, exist_ok=True)
    (destination/"pyproject.toml").write_text('[tool.pyright]\ninclude = ["."]\n')
    (destination/"people.py").write_text('class Person:\n    def greet(self) -> str:\n        return "Hello"\n')
    (destination/"use.py").write_text('from people import Person\nperson = Person()\nresult = person.greet()\n')
    commands=[
        str(codex),"app-server","--stdio",
        "-c",'mcp_servers.simple-lsp.command="' + str(binary) + '"',
        "-c",'mcp_servers.simple-lsp.args=["--workspace","' + str(destination) + '","--tool-profile","full","--result-format","both"]',
    ]
    with tempfile.TemporaryDirectory(prefix="simple-lsp-python-matrix-home-") as temporary:
        env=os.environ.copy()
        env["CODEX_HOME"]=temporary
        app=SUPPORT.AppServer(commands,destination,120,env)
        try:
            app.request("initialize",{"clientInfo":{"name":"simple-lsp-python-declaration","version":"1"}})
            app.notify("initialized")
            thread=app.request("thread/start",{
                "cwd":str(destination),"ephemeral":True,"approvalPolicy":"never","sandbox":"read-only"
            })["thread"]["id"]
            arguments={"path":"use.py","line":2,"column":10,"language":"python"}
            start=time.monotonic()
            result=app.request("mcpServer/tool/call",{
                "threadId":thread,"server":"simple-lsp","tool":"get_declaration","arguments":arguments
            })
            elapsed=round(time.monotonic()-start,3)
            is_error=bool(result.get("isError"))
            obj=result.get("structuredContent") or {}
            text="\n".join(str(x.get("text","")) for x in result.get("content",[]))
            if not obj and text:
                obj=json.loads(text)
            assert not is_error and obj.get("meta",{}).get("complete") is True, obj
            locations=obj.get("locations",[])
            assert len(locations)==1 and locations[0]["path"]=="people.py", obj
            assert locations[0]["range"]["start"]["line"]==1, obj
            record={"tool":"get_declaration","language":"python","arguments":arguments,
                    "isError":False,"result_bytes":len(text.encode()),
                    "elapsed_sec":elapsed,"semantic_nonempty":True,
                    "locations":len(locations),"meta":obj["meta"],"preview":text[:900]}
            print("get_declaration python error",False,"bytes",len(text.encode()),"sec",elapsed,flush=True)
            return record
        finally:app.close()


def verify_contracts(go: list[dict], python_decl: dict, fixture: Path) -> None:
    if len(go)!=22 or len({r["tool"] for r in go})!=22:
        raise AssertionError("Go fixture did not invoke all 22 advertised tools")
    for record in go:
        if record["tool"]=="get_declaration":
            if not record.get("isError") or "METHOD_NOT_SUPPORTED" not in record.get("preview",""):
                raise AssertionError("gopls declaration capability was misrepresented")
        elif record.get("isError") or not record.get("semantic_nonempty"):
            raise AssertionError("MCP operation is unsuccessful/incomplete: "+repr(record))
    if not python_decl.get("semantic_nonempty"):
        raise AssertionError("Python declaration did not resolve")
    by_name={r["tool"]:r for r in go}
    for name in ("rename_symbol","format_document","apply_code_action"):
        if '"applied":true' not in by_name[name]["preview"]:
            raise AssertionError(name+" claimed no applied edit")
    models=(fixture/"models.go").read_text()
    if "MakePerson" in models or "CreatePerson" not in models:
        raise AssertionError("rename did not update actual source content")
    if "func NeedsFormat() int" not in (fixture/"format.go").read_text():
        raise AssertionError("format was not applied to the source")
    if 'import (' in (fixture/"actions.go").read_text():
        raise AssertionError("code action was not applied to the source")
    if "UnusedImport" not in by_name["get_diagnostics"].get("preview",""):
        raise AssertionError("diagnostics omitted a real compiler error")
    impact=by_name["analyze_impact"]
    if impact.get("meta",{}).get("complete") is not True:
        raise AssertionError("impact graph is incomplete")



if __name__=="__main__":
    parser=argparse.ArgumentParser()
    parser.add_argument("--mcp",type=Path,required=True)
    parser.add_argument("--codex",type=Path,required=True)
    parser.add_argument("--output",type=Path,required=True)
    args=parser.parse_args()
    out=args.output.resolve()
    out.mkdir(parents=True,exist_ok=False)
    go=run(args.mcp.resolve(),args.codex.resolve(),out/"fixture")
    python_decl=check_python_declaration(args.mcp.resolve(),args.codex.resolve(),out/"python")
    verify_contracts(go,python_decl,out/"fixture")
    result=go+[python_decl]
    (out/"matrix.json").write_text(json.dumps(result,ensure_ascii=False,indent=2)+"\n")
    print("TOTAL_CALLS",len(result),"DISTINCT_TOOLS",len({r["tool"] for r in result}),
          "SUCCESSFUL_MEANINGFUL",sum(x.get("semantic_nonempty",False) for x in result),
          "GOPLS_UNSUPPORTED",sum(x.get("isError",False) for x in result))
