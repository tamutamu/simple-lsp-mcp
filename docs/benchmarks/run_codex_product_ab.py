#!/usr/bin/env python3
"""Serial Codex-only versus Codex+simple-lsp MCP real-development benchmark.

Isolates every trial. The sole experimental difference between arms is the
presence of the simple-lsp MCP connector. Does not change the original repo.
Run: PRODUCT_AB_CODEX_HOME=... PRODUCT_AB_MCP=... python docs/benchmarks/run_codex_product_ab.py --cases T35,T43 --repetitions 3
"""
from __future__ import annotations

import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import select
import shutil
import subprocess
import sys
import time
from typing import Any

ROOT=Path(__file__).resolve().parents[2]
SCRIPT=Path(__file__).resolve().parent/"codex_product_ab_cases.py"
spec=importlib.util.spec_from_file_location("product_cases",SCRIPT)
catalog=importlib.util.module_from_spec(spec)
sys.modules[spec.name]=catalog
spec.loader.exec_module(catalog)
catalog.validate()

NODE=os.environ.get("PRODUCT_AB_NODE") or shutil.which("node") or ""
CODEX=os.environ.get("PRODUCT_AB_CODEX") or shutil.which("codex") or ""
MCP=os.environ.get("PRODUCT_AB_MCP") or shutil.which("simple-lsp-mcp") or ""
PROFILE=os.environ.get("PRODUCT_AB_PROFILE", "full")
GO=os.environ.get("PRODUCT_AB_GO") or shutil.which("go") or ""
TSC=os.environ.get("PRODUCT_AB_TSC") or shutil.which("tsc") or ""
VENVPY=os.environ.get("PRODUCT_AB_PYTHON") or sys.executable
BASE=Path(os.environ.get("PRODUCT_AB_OUTPUT", str(ROOT/".scratch/product-ab-evaluation"))).resolve()
FROZEN=BASE/"frozen-sources"
CODEX_HOME=Path(os.environ.get("PRODUCT_AB_CODEX_HOME", "")).expanduser().resolve() if os.environ.get("PRODUCT_AB_CODEX_HOME") else None
COMMON_AGENTS="""# Benchmark work rules
You are fixing code in an isolated copy of a real project. Make the smallest
correct change, preserve behavior except as requested, and verify with relevant
tests. The same rules apply whether a semantic LSP connector is present or not.
Use tools to get only the evidence needed for the task, not for quotas.
When the task is a cross-file rename of a declaration whose spelling collides
with unrelated declarations, and the `simple-lsp` MCP is connected, use its
read-only `preview_rename` to identify the precise edit set before making
changes; then use `rename_symbol(apply=true)` if safe and available, or a
normal editor if that is safer. The LSP computes semantic identity; ordinary
text matching cannot distinguish declarations with the same name. When the
MCP is not connected, proceed normally with search, editing, tests and Git.
For literal text, obvious compiler errors and unrelated tasks, do not invoke
an LSP without a concrete semantic question. This is task-conditional guidance,
not a blanket requirement for an LSP call or a tool-count target.
Do not change CI, deployment, unrelated formatting, build configuration or
tests to silence a genuine defect. Do not install dependencies. No network use.
"""
TYPE_DIAGNOSTICS_AGENTS=COMMON_AGENTS.replace(
    "When the task is a cross-file rename of a declaration whose spelling collides\n"
    "with unrelated declarations, and the `simple-lsp` MCP is connected, use its\n"
    "read-only `preview_rename` to identify the precise edit set before making\n"
    "changes; then use `rename_symbol(apply=true)` if safe and available, or a\n"
    "normal editor if that is safer. The LSP computes semantic identity; ordinary\n"
    "text matching cannot distinguish declarations with the same name. When the\n"
    "MCP is not connected, proceed normally with search, editing, tests and Git.\n"
    "For literal text, obvious compiler errors and unrelated tasks, do not invoke\n"
    "an LSP without a concrete semantic question. This is task-conditional guidance,\n"
    "not a blanket requirement for an LSP call or a tool-count target.",
    "When fixing a Go type error in a known source file, warmed LSP diagnostics\n"
    "can provide exact compiler mismatch messages and positions immediately.\n"
    "If simple-lsp is connected and no equivalent diagnostic was yet obtained,\n"
    "prefer get_diagnostics(path=...) over a full preliminary build to locate\n"
    "the error; then inspect and edit only the affected code. Run real tests\n"
    "after the edit. With no LSP, use the ordinary compiler and code tools.\n"
    "Avoid repeated diagnostic reads or tool calls that add no new information."
)
assert TYPE_DIAGNOSTICS_AGENTS != COMMON_AGENTS
SEMANTIC_REFERENCES_AGENTS = COMMON_AGENTS + (
    "\nChoose the cheapest reliable evidence. When a task explicitly asks for"
    " a literal Go selector such as tools.New(...), search that exact"
    " spelling with ordinary code search first; LSP is only needed when"
    " aliases, ambiguous symbols, or indirect uses make semantics necessary."
    " For genuine semantic callers, use simple-lsp"
    " find_references(path=...,symbol_path=...,relation_view=lines) if"
    " connected. If only production callers matter, set"
    " exclude_test_files=true and include_declaration=false; the filter"
    " scope is disclosed. Do not use inspect_symbol(related=references)"
    " for exhaustive results: it caps each relation at 20. Reuse complete"
    " results instead of rereading unchanged source. With no LSP,"
    " use ordinary tools. There is no LSP call quota.\n"
)

ENV=os.environ.copy()
if CODEX_HOME is not None:
 ENV["CODEX_HOME"]=str(CODEX_HOME)
paths=[str(Path(x).parent) for x in (GO,NODE,CODEX,TSC) if x and Path(x).is_absolute()]
ENV["PATH"]=os.pathsep.join(paths+[ENV.get("PATH","")])
ENV["GOCACHE"]=str(BASE/"cache-go")
ENV["TMPDIR"]=str(BASE/"temp")
ENV["PYTHONPATH"]="backend"

def run_command(command:list[str], cwd:Path, timeout:int=120)->subprocess.CompletedProcess:
 return subprocess.run(command,cwd=cwd,env=ENV,capture_output=True,text=True,timeout=timeout)

def copy_project(source:Path,target:Path):
 skip={".git",".scratch",".local","node_modules","__pycache__",".pytest_cache",".venv","dist","build",".next"}
 shutil.copytree(source,target,ignore=lambda folder, files:sorted(set(files)&skip),ignore_dangling_symlinks=True)

def make_baseline(case:dict, trial:Path):
 if trial.exists():raise RuntimeError("refuse to overwrite existing "+str(trial))
 frozen=FROZEN/("go" if case["seed"]=="go" else "fastapi")
 assert frozen.exists(), "source snapshot was not frozen before A/B"
 copy_project(frozen,trial)
 if case["seed"]=="ts":
  for rel,contents in catalog.TS_BASE.items():
   p=trial/rel;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(contents)
 if case["seed"]=="py":
  for rel,contents in catalog.PY_BASE.items():
   p=trial/rel;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(contents)
 for op in case.get("edits",[]):
  rel,before,after=op.split("|",2)
  path=trial/rel
  text=path.read_text()
  if text.count(before)!=1:raise AssertionError(f"seed mutation not unique: {rel}: {text.count(before)}")
  path.write_text(text.replace(before,after,1))
 for file in ("AGENTS.md","CLAUDE.md"):
  p=trial/file
  if p.exists():p.unlink()
 (trial/"AGENTS.md").write_text(COMMON_AGENTS)
 for command in (["git","init","-q"],["git","add","-A"],["git","-c","user.name=Benchmark","-c","user.email=bench@example.invalid","commit","-qm","frozen-task-baseline"]):
  proc=run_command(command,trial)
  if proc.returncode:raise RuntimeError("cannot freeze baseline: "+proc.stderr[:700])
 return run_command(["git","rev-parse","HEAD"],trial).stdout.strip()

def agent_workspace_environment(case:dict, work:Path, warm_cache:bool)->tuple[dict,float,int]:
 """Use a sandbox-writable, prewarmed Go cache with independent A/B copies.
 A missing/intentional compile error is recorded, never called a success.
 """
 if not warm_cache:
  return ENV,0.0,0
 env=ENV.copy()
 cache=work/".bench-go-cache"
 temp=work/".bench-temp"
 cache.mkdir()
 temp.mkdir()
 env["GOCACHE"]=str(cache)
 env["TMPDIR"]=str(temp)
 # The cache must not alter Git's acceptance diff or pollute literal rg searches.
 with (work/".git/info/exclude").open("a") as ignore:
  ignore.write("\n.bench-go-cache/\n.bench-temp/\n")
 if case["seed"]!="go":
  return env,0.0,0
 start=time.monotonic()
 try:
  result=subprocess.run([GO,"test","./...","-run","^$"],cwd=work,env=env,capture_output=True,text=True,timeout=160)
  status=result.returncode
 except subprocess.TimeoutExpired:
  status=124
 return env,round(time.monotonic()-start,3),status


def read_events(trace:Path)->list[dict]:
 result=[]
 if trace.exists():
  with trace.open() as file:
   for line in file:
    try:result.append(json.loads(line)["event"])
    except (KeyError,json.JSONDecodeError):continue
 return result

def event_metrics(events:list[dict])->dict:
 token={}
 call=[]
 cmd=[]
 final=""
 for event in events:
  method=event.get("method")
  if method=="thread/tokenUsage/updated":
   token=event.get("params",{}).get("tokenUsage",{}).get("total",{})
  item=event.get("params",{}).get("item",{})
  if method=="item/completed" and item.get("type")=="mcpToolCall":
   content=(item.get("result") or {}).get("content") or []
   txt=content[0].get("text","") if content else ""
   call.append({"tool":item.get("tool"),"arguments":item.get("arguments"),
    "bytes":len(txt.encode()),"status":item.get("status"),"is_error":bool((item.get("result") or {}).get("isError"))})
  if method=="item/completed" and item.get("type")=="commandExecution":
   cmd.append({"command":str(item.get("command") or ""),"bytes":len((item.get("aggregatedOutput") or "").encode()),
    "exit_code":item.get("exitCode")})
  if method=="item/completed" and item.get("type")=="agentMessage":
   final=str(item.get("text") or final)
 return {"tokens":token,"noncached_input_tokens":token.get("inputTokens",0)-token.get("cachedInputTokens",0),
  "mcp_calls":call,"cli_calls":cmd,"final_answer":final,
  "rg_calls":sum("rg " in x["command"] or "grep " in x["command"] for x in cmd),
  "read_calls":sum("sed " in x["command"] or "cat " in x["command"] or "head " in x["command"] for x in cmd)}

def execute_agent(case:dict, trial:Path, arm:str, directory:Path, baseline_hash:str, timeout:int, warm_cache:bool)->dict:
 log=directory/(arm+"-trace.jsonl")
 errfile=directory/(arm+"-server-stderr.txt")
 command=([NODE,CODEX] if CODEX.endswith(".js") else [CODEX])+["app-server","--stdio","-c","sandbox_workspace_write.network_access=false"]
 if arm=="mcp":
  command+=["-c",'mcp_servers.simple-lsp.command="'+MCP+'"',
    "-c",'mcp_servers.simple-lsp.args=["--workspace","'+str(trial)+'","--tool-profile","'+PROFILE+'","--result-format","readable"]']
 agent_env,cache_warm_seconds,cache_warm_exit=agent_workspace_environment(case,trial,warm_cache)
 started=time.monotonic()
 with log.open("w") as trace,errfile.open("w") as errs:
  proc=subprocess.Popen(command,cwd=trial,env=agent_env,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=errs,bufsize=0)
  rid=0
  def send(method,params=None,notification=False):
   nonlocal rid
   rid+=1
   packet={"jsonrpc":"2.0","method":method}
   if not notification:packet["id"]=rid
   if params is not None:packet["params"]=params
   proc.stdin.write((json.dumps(packet,ensure_ascii=False)+"\n").encode())
   proc.stdin.flush()
   return rid
  def recv(deadline):
   while time.monotonic()<deadline:
    if proc.poll() is not None:raise RuntimeError("Codex app-server exited "+str(proc.returncode))
    ready,_,_=select.select([proc.stdout],[],[],0.5)
    if ready:
     line=proc.stdout.readline()
     if not line:raise RuntimeError("Codex app-server pipe closed")
     try:ev=json.loads(line)
     except (UnicodeError,json.JSONDecodeError):continue
     trace.write(json.dumps({"received":time.time(),"event":ev},ensure_ascii=False)+"\n")
     trace.flush()
     method=ev.get("method","")
     if "id" in ev and method=="mcpServer/elicitation/request":
      params=ev.get("params") or {}
      meta=params.get("_meta") or {}
      args=meta.get("tool_params") or {}
      toolpath=args.get("path") or ""
      # Approve only explicit MCP file edits *inside the isolated trial*.
      writable=meta.get("codex_approval_kind")=="mcp_tool_call"
      relative=not Path(toolpath).is_absolute() and not any(p==".." for p in Path(toolpath).parts)
      permitted=writable and (not toolpath or relative) and params.get("serverName")=="simple-lsp"
      result={"action":"accept","content":{}} if permitted else {"action":"decline"}
      proc.stdin.write((json.dumps({"jsonrpc":"2.0","id":ev["id"],"result":result})+"\n").encode());proc.stdin.flush()
     elif "id" in ev and method.endswith("requestApproval"):
      proc.stdin.write((json.dumps({"jsonrpc":"2.0","id":ev["id"],"result":{"decision":"decline"}})+"\n").encode());proc.stdin.flush()
     return ev
   raise TimeoutError("app-server event timed out")
  def waitid(request_id,deadline):
   for _ in range(5000):
    e=recv(deadline)
    if e.get("id")==request_id:
     if e.get("error"):raise RuntimeError("RPC error: "+str(e["error"])[:600])
     return e.get("result",{})
   raise TimeoutError("response not found")
  try:
   deadline=time.monotonic()+timeout
   waitid(send("initialize",{"clientInfo":{"name":"product-ab","version":"1.0"}}),deadline)
   send("initialized",notification=True)
   reply=waitid(send("thread/start",{"cwd":str(trial),"model":"gpt-5.6-sol","ephemeral":True,
     "approvalPolicy":"never","sandbox":"workspace-write"}),deadline)
   thread=reply["thread"]["id"]
   # The language server stays warm in normal long-lived coding sessions.
   # Prewarm the treatment's server on an unrelated file before starting the
   # agent clock. This is not an agent-visible tool selection or trial outcome.
   # Preserve the warm-up duration separately from task wall time.
   prewarm_seconds=0.0
   if arm=="mcp":
    target={"go":"internal/core/types.go", "ts":"frontend/src/bench/types.ts", "py":"backend/benchdomain/types.py"}[case["seed"]]
    warm_started=time.monotonic()
    prewarm=waitid(send("mcpServer/tool/call",{
      "threadId":thread,"server":"simple-lsp","tool":"get_document_symbols",
      "arguments":{"path":target}}),time.monotonic()+min(120,timeout))
    if prewarm.get("isError"):
     raise RuntimeError("MCP prewarm failed: "+str(prewarm)[:1000])
    prewarm_seconds=time.monotonic()-warm_started
   deadline=time.monotonic()+timeout
   taskid=waitid(send("turn/start",{"threadId":thread,
     "input":[{"type":"text","text":case["prompt"]}],"effort":"low"}),deadline)["turn"]["id"]
   begin=time.monotonic()
   for _ in range(20000):
    event=recv(deadline)
    if event.get("method")=="turn/completed" and event.get("params",{}).get("turn",{}).get("id")==taskid:
     status=event["params"]["turn"].get("status")
     break
    if event.get("method")=="turn/failed" and event.get("params",{}).get("turn",{}).get("id")==taskid:
     status="failed"
     break
   else:raise TimeoutError("turn did not complete")
   duration=round(time.monotonic()-begin,3)
  finally:
   proc.terminate()
   try:proc.wait(timeout=4)
   except subprocess.TimeoutExpired:proc.kill()
 events=read_events(log)
 result=event_metrics(events)
 result.update({"status":status,"work_seconds":duration,"wall_seconds":round(time.monotonic()-started,3),
   "baseline_hash":baseline_hash,"arm":arm,"case_id":case["id"],
   "initial_snapshot":case["seed"],"model":"gpt-5.6-sol","effort":"low", "prewarm_seconds":round(prewarm_seconds,3), "cache_warm_seconds":cache_warm_seconds, "cache_warm_exit":cache_warm_exit})
 return result

def grading(case:dict,work:Path,final:str)->dict:
 checks=[]
 def check(label,command,timeout=90,env=None):
  global ENV
  newenv=ENV.copy()
  if env:newenv.update(env)
  try:p=subprocess.run(command,cwd=work,env=newenv,capture_output=True,text=True,timeout=timeout)
  except subprocess.TimeoutExpired:
   checks.append({"name":label,"ok":False,"timeout":True})
   return False
  okay=p.returncode==0
  checks.append({"name":label,"ok":okay,"exit":p.returncode,"stdout_tail":p.stdout[-600:],"stderr_tail":p.stderr[-400:]})
  return okay
 changed=run_command(["git","diff","--name-only"],work).stdout.splitlines()
 check("diff_clean",["git","diff","--check"],15)
 if case.get("read_only"):
  checks.append({"name":"read_only","ok":len(changed)==0,"changed":changed})
  conditions={
   "T33":["internal/tools/engine.go","Relationship","internal/mcpserver/"],
   "T34":["internal/tools","tools.New"],
   "T44":["visible_orders","owner_invoice","!="],
  }
  needles=conditions[case["id"]]
  checks.append({"name":"answer_evidence","ok":all(x.lower() in final.lower() for x in needles),
    "missing":[x for x in needles if x.lower() not in final.lower()]})
 else:
  if case["seed"]=="go":
   check("go_test",[GO,"test","./..."],180)
   check("go_vet",[GO,"vet","./..."],130)
  if case["seed"]=="ts":
   check("typescript",[NODE,TSC,"-p","frontend/tsconfig.bench.json"],35)
   check("node_tests",[NODE,"--test","frontend/src/bench/billing.test.mjs"],35)
  if case["seed"]=="py":
   check("python_tests",[VENVPY,"-m","unittest","discover","-s","backend/tests/benchdomain","-p","test_*.py"],30,{"PYTHONPATH":str((work/"backend").resolve())})
  source_checks={
   "T35":("internal/tools/engine.go","MaxResults:      cfg.MaxResults,"),
   "T36":("internal/tools/rename.go",'result["applied"] = true'),
   "T37":("frontend/src/bench/billing.ts","return item.unitPriceCents * item.quantity;"),
   "T38":("frontend/src/bench/billing.ts","Math.round(subtotal * taxBps / 10000)"),
   "T39":("backend/benchdomain/pricing.py","return Invoice(subtotal, discount, subtotal - discount)"),
   "T40":("backend/benchdomain/reporting.py","def owner_summary("),
   "T41":("backend/benchdomain/orders.py","bool(order.description.strip())"),
   "T42":("frontend/src/bench/repository.ts","findAllBySku("),
   "T43":("internal/tools/engine.go","func NewEngine("),
   "T45":("frontend/src/bench/billing.ts","InvoiceSummary"),
   "T46":("backend/benchdomain/pricing.py","+ 5000"),
   "T47":("internal/tools/engine.go","MaxResults:      cfg.MaxResults,"),
  }
  target,marker=source_checks[case["id"]]
  checks.append({"name":"expected_contract","ok":marker in (work/target).read_text()})
  if case["id"]=="T43":
   check("rename_new_calls",["rg","-q",r"\btools\.NewEngine\(","cmd","internal"])
   result=run_command(["rg","-n",r"\btools\.New\(","cmd","internal"],work)
   checks.append({"name":"no_old_constructor_call","ok":result.returncode==1,"output":result.stdout[:300]})
  if case["id"]=="T47":
   checks.append({"name":"runtime_contract","ok":'result["applied"] = true' in (work/"internal/tools/rename.go").read_text()})
  if case["id"]=="T40":
   # Held-out tests are not placed in the agent workspace.
   hidden="""from benchdomain.types import Order
from benchdomain.reporting import owner_summary
orders=[Order("1","alice",100,"yes"),Order("2","bob",200,"  "),Order("3","alice",300,None)]
assert owner_summary(orders,"alice")=={"total":2,"with_description":1,"without_description":1}
assert owner_summary(orders,"alice",True)=={"total":3,"with_description":1,"without_description":2}
"""
   check("hidden_owner_summary",[VENVPY,"-c",hidden],30,{"PYTHONPATH":str((work/"backend").resolve())})
  if case["id"]=="T42":
   hidden='''import { InMemoryInventory } from "./frontend/src/bench/repository.ts";
import assert from "node:assert/strict";
const repo=new InMemoryInventory([{sku:"a",quantity:1,unitPriceCents:50},{sku:"b",quantity:1,unitPriceCents:60},{sku:"a",quantity:2,unitPriceCents:70}]);
assert.deepEqual(repo.findAllBySku("a").map(x=>x.unitPriceCents),[50,70]);
assert.deepEqual(repo.findAllBySku("missing"),[]);
'''
   check("hidden_inventory_interface",[NODE,"--input-type=module","-e",hidden],35)
  if case["id"] in ("T38","T46"):
   # A meaningful 0.5 cent tie exposes floor-vs-round errors.
   if case["id"]=="T38":
    check("hidden_round_half_up",[NODE,"--input-type=module","-e",'''import { invoiceSummary } from "./frontend/src/bench/billing.ts"; import assert from "node:assert/strict"; assert.equal(invoiceSummary([{sku:"a",quantity:1,unitPriceCents:105}],50).taxCents,1);'''],25)
   else:
    check("hidden_discount_round_half_up",[VENVPY,"-c","from benchdomain.pricing import discount_cents; assert discount_cents(105,50)==1"],25,{"PYTHONPATH":str((work/"backend").resolve())})
  allowed={
   "T35":["internal/tools/engine.go"],"T36":["internal/tools/rename.go"],
   "T37":["frontend/src/bench/"],"T38":["frontend/src/bench/"],"T39":["backend/benchdomain/"],
   "T40":["backend/benchdomain/","backend/tests/benchdomain/"],
   "T41":["backend/benchdomain/"],"T42":["frontend/src/bench/"],
   "T43":["cmd/","internal/"],"T45":["frontend/src/bench/"],
   "T46":["backend/benchdomain/"],"T47":["internal/tools/"],
  }
  checks.append({"name":"no_unrelated_changes","ok":all(any(f.startswith(prefix) for prefix in allowed[case["id"]]) for f in changed),
   "changed_files":changed})
 return {"pass":all(x.get("ok",False) for x in checks),"checks":checks,"changed_files":changed}

def main():
 parser=argparse.ArgumentParser()
 parser.add_argument("--cases",default="all")
 parser.add_argument("--repetitions",type=int,default=2)
 parser.add_argument("--timeout",type=int,default=360)
 parser.add_argument("--policy", choices=("semantic-rename","type-diagnostics","semantic-references"), default="semantic-rename")
 parser.add_argument("--cache-policy",choices=("warm-workspace","legacy-external"),default="warm-workspace")
 args=parser.parse_args()
 if not CODEX or not MCP or not GO:
  parser.error("set PRODUCT_AB_CODEX, PRODUCT_AB_MCP and PRODUCT_AB_GO or install binaries on PATH")
 if CODEX_HOME is None or not CODEX_HOME.is_dir():
  parser.error("set PRODUCT_AB_CODEX_HOME to an authenticated, isolated Codex home (with no unrelated MCP servers)")
 if args.repetitions<1 or args.timeout<30:
  parser.error("repetitions must be >=1 and timeout >=30")
 global COMMON_AGENTS
 COMMON_AGENTS = (
  TYPE_DIAGNOSTICS_AGENTS if args.policy == "type-diagnostics"
  else SEMANTIC_REFERENCES_AGENTS if args.policy == "semantic-references"
  else COMMON_AGENTS
 )
 if PROFILE not in ("full", "coding"):
  parser.error("PRODUCT_AB_PROFILE must be full or coding")
 policy=args.policy
 cache_policy=args.cache_policy
 args=argparse.Namespace(cases=args.cases,repetitions=args.repetitions,timeout=args.timeout)
 case_ids={x["id"] for x in catalog.CASES} if args.cases=="all" else set(args.cases.split(","))
 cases=[x for x in catalog.CASES if x["id"] in case_ids]
 assert len(cases)==len(case_ids),(case_ids,{x["id"] for x in cases})
 BASE.mkdir(parents=True,exist_ok=True)
 (BASE/"cache-go").mkdir(exist_ok=True)
 (BASE/"temp").mkdir(exist_ok=True)
 # Freeze only source projects needed by the requested cases. Once frozen, no source edits are allowed. No worktree changes after
 # this point can change the A/B inputs. Both arms use an identical seed commit.
 FROZEN.mkdir(exist_ok=False)
 if any(c["seed"]=="go" for c in cases):copy_project(ROOT,FROZEN/"go")
 if any(c["seed"] in ("ts","py") for c in cases):
  if not catalog.FASTAPI.is_dir():parser.error("PRODUCT_AB_FASTAPI must name a pinned FastAPI source checkout for TS/Python cases")
  copy_project(catalog.FASTAPI,FROZEN/"fastapi")
 if any(c["seed"]=="ts" for c in cases) and (not NODE or not TSC):
  parser.error("TypeScript cases require node and tsc (PRODUCT_AB_NODE/PRODUCT_AB_TSC)")
 (BASE/"manifest.json").write_text(json.dumps({"cases":[{k:v for k,v in item.items() if k not in ("edits",)} for item in cases],
  "repetitions":args.repetitions,"model":"gpt-5.6-sol","effort":"low",
  "common_policy":COMMON_AGENTS,"policy":policy,"tool_profile":PROFILE,"cache_policy":cache_policy,"arms":["baseline","mcp"],
  "codex_binary":CODEX,"mcp_binary":MCP,"source_root":str(ROOT),
  "codex_version":run_command([CODEX,"--version"],ROOT).stdout.strip()},ensure_ascii=False,indent=2)+"\n")
 summaries=[]
 for case in cases:
  for rep in range(1,args.repetitions+1):
   order=("baseline","mcp") if rep%2 else ("mcp","baseline")
   trial=BASE/f"{case['id']}-{case['name']}-rep{rep}"
   trial.mkdir(exist_ok=False)
   seed=trial/"frozen-task-seed"
   fingerprint=make_baseline(case,seed)
   for arm in order:
    work=trial/arm
    print("START",case["id"],rep,arm,flush=True)
    try:
     shutil.copytree(seed,work)
     assert run_command(["git","rev-parse","HEAD"],work).stdout.strip()==fingerprint
     assert not run_command(["git","status","--porcelain"],work).stdout.strip()
     r=execute_agent(case,work,arm,trial,fingerprint,args.timeout,cache_policy=="warm-workspace")
     grade=grading(case,work,r["final_answer"])
     r["grade"]=grade
     if r.get("status")!="completed":
      r["grade"]["pass"]=False
     print("DONE",case["id"],rep,arm,"seconds",r["work_seconds"],"PASS",grade["pass"],
      "MCP",len(r["mcp_calls"]),"CLI",len(r["cli_calls"]),flush=True)
    except Exception as err:
     r={"case_id":case["id"],"arm":arm,"repetition":rep,"error":repr(err),"grade":{"pass":False}}
     print("ERROR",case["id"],rep,arm,repr(err),flush=True)
    r["repetition"]=rep
    (trial/(arm+"-results.json")).write_text(json.dumps(r,indent=2,ensure_ascii=False)+"\n")
    summaries.append({"id":case["id"],"rep":rep,"arm":arm,"passed":r["grade"]["pass"],
     "seconds":r.get("work_seconds"),"mcp_calls":len(r.get("mcp_calls",[])),
     "noncached_input_tokens":r.get("noncached_input_tokens"),
     "error":r.get("error")})
    (BASE/"summary.json").write_text(json.dumps(summaries,indent=2,ensure_ascii=False)+"\n")
 print("RESULTS",json.dumps(summaries,ensure_ascii=False),flush=True)

if __name__=="__main__":main()
