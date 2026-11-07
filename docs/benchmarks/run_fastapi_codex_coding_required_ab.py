#!/usr/bin/env python3
"""Codex A/B: same pinned FastAPI source and identical AGENTS.md, MCP on/off."""
import argparse, csv, hashlib, json, os, select, shutil, statistics, subprocess, time
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor, as_completed

BASE=Path(__file__).resolve().parents[2]
TASKS=json.loads((BASE/"docs/benchmarks/fastapi-template-tasks.json").read_text())
GRADER=BASE/"docs/benchmarks/fastapi-template-grader.py"
SHA="b1125d8839482cb7bbae603ddc6d0081bf87532e"
POLICY="""# Matched task A/B: same instructions in both arms
Perform the requested source edit, and keep scope minimal. When Simple LSP MCP
is connected, BEFORE changing code you must obtain relevant real source with
inspect_symbol(view="edit"). For search use path=backend/app/api/routes/items.py
symbol_path=read_items. For paging use one inspect_symbol request with targets
for read_items in items.py and read_users in users.py. For access use one
inspect_symbol targets request for read_item, update_item and delete_item,
all in backend/app/api/routes/items.py. Use the returned code when planning
changes, and don't reread the same code with sed/cat without an actual missing
piece of context. Do not call any other MCP tools without a relevant need.
When MCP is NOT connected, work normally with ordinary reading/search tools;
no special prerequisites.
The benchmark worktree does not have pytest dependencies. Don't run uv,
pytest, or install dependencies there; an independent grader uses a separate
Python environment for acceptance tests. Validate with python -m py_compile,
ruff if locally available, and git diff --check.
Do not edit AGENTS.md, dependencies, or generated files.
"""
SUFFIX="Follow project AGENTS.md. Work only inside this pinned project checkout. Keep scope minimal; do not edit generated clients, AGENTS.md or dependencies. Run reasonable checks. Finish with a concise result."

def call(argv, cwd=None, **kw):
    return subprocess.run(argv, cwd=cwd, capture_output=True, text=True, **kw)

def prepare(source, dst):
    if dst.exists():raise RuntimeError("Worktree already exists: "+str(dst))
    dst.mkdir(parents=True)
    proc=subprocess.Popen(["git","-C",str(source),"archive","HEAD"], stdout=subprocess.PIPE)
    subprocess.run(["tar","-x","-C",str(dst)],stdin=proc.stdout,check=True)
    proc.stdout.close()
    if proc.wait():raise RuntimeError("git archive failed")
    for name in ("AGENTS.md","CLAUDE.md",".mcp.json",".codex",".claude"):
        x=dst/name
        if x.is_dir():shutil.rmtree(x)
        elif x.exists():x.unlink()
    (dst/"AGENTS.md").write_text(POLICY)
    for cmd in (["git","init","-q"],["git","add","-A"],
                ["git","-c","user.name=Benchmark","-c","user.email=benchmark@example.invalid",
                 "commit","-qm","baseline"]):
        p=call(cmd,cwd=dst)
        if p.returncode:raise RuntimeError(p.stderr)

def _has_nested_error(value):
    """Fail closed on MCP application errors wrapped in structured results."""
    if isinstance(value, dict):
        if "error" in value or value.get("isError") or value.get("is_error"):
            return True
        return any(_has_nested_error(part) for part in value.values())
    if isinstance(value, list):
        return any(_has_nested_error(part) for part in value)
    return False



def _has_meaningful_payload(value):
    """A completed MCP response is not useful when its JSON body is empty.

    Empty arrays, objects, null, booleans and success-only envelopes cannot
    establish that an agent received semantic source information.
    """
    if isinstance(value, str):
        value = value.strip()
        if not value or value.lower().startswith(
                ("error:", "error ", "mcp error", "lsp error")):
            return False
        try:
            decoded = json.loads(value)
        except ValueError:
            return True  # Non-JSON source text is a valid MCP result.
        return _has_meaningful_payload(decoded)
    if isinstance(value, list):
        return any(_has_meaningful_payload(part) for part in value)
    if isinstance(value, dict):
        return any(_has_meaningful_payload(part) for key, part in value.items()
                   if key not in ("success", "ok", "status", "isError", "is_error"))
    return False


def successful_mcp_call(item):
    """Count a call only if its trace contains a non-error MCP response.

    The coding profile uses --result-format text: an LSP application error
    can live solely in content[0].text, with no structured_content. Merely
    seeing item.completed (or an empty/missing result) is not proof of use.
    """
    if item.get("status") != "completed" or item.get("error"):
        return False
    result=item.get("result")
    if isinstance(result,str):
        try: result=json.loads(result)
        except ValueError: return False
    if not isinstance(result,dict) or _has_nested_error(result):
        return False
    structured=result.get("structured_content",result.get("structuredContent"))
    content=result.get("content")
    if not isinstance(content,list):
        return isinstance(structured,dict) and _has_meaningful_payload(structured)
    useful=False
    for part in content:
        if not isinstance(part,dict):
            continue
        if part.get("type")=="text":
            text=part.get("text")
            if not isinstance(text,str) or not text.strip():
                continue
            try: payload=json.loads(text)
            except ValueError:
                payload=text
            if _has_nested_error(payload):
                return False
            useful = useful or _has_meaningful_payload(payload)
        elif part.get("type") in ("image","resource","resource_link","audio"):
            useful=True
    return useful or (isinstance(structured,dict) and _has_meaningful_payload(structured))



def trace_mcp_calls(events):
    """Return every attempted Simple LSP call and only verified successful ones.

    Codex emits item.started, item.updated and item.completed/item.failed for
    the same id. Counting only item.completed hides failed and interrupted
    attempts, corrupting the baseline-is-MCP-free audit and call efficiency.
    A missing id cannot safely be coalesced with another event.
    """
    attempts = {}
    successful = {}
    for index, event in enumerate(events):
        kind = event.get("type")
        if kind not in ("item.started", "item.updated", "item.completed", "item.failed"):
            continue
        item = event.get("item")
        if not isinstance(item, dict) or item.get("type") != "mcp_tool_call":
            continue
        if item.get("server") != "simple-lsp":
            continue
        identifier = item.get("id")
        key = identifier if isinstance(identifier, str) and identifier else ("unknown", index)
        if key not in attempts or kind in ("item.completed", "item.failed"):
            attempts[key] = item
        if kind == "item.failed":
            successful.pop(key, None)
        elif kind == "item.completed":
            if successful_mcp_call(item):
                successful[key] = item
            else:
                successful.pop(key, None)
    return list(attempts.values()), list(successful.values())


def trace_tool_attempts(events):
    """Count attempted commands and identical calls, including failed attempts.

    Lifecycle events share an item ID. Exact repeats are a diagnostic proxy,
    not proof that a repeated call was unnecessary.
    """
    attempts = {}
    for index, event in enumerate(events):
        if event.get("type") not in (
                "item.started", "item.updated", "item.completed", "item.failed"):
            continue
        item = event.get("item")
        if not isinstance(item, dict) or item.get("type") not in (
                "command_execution", "mcp_tool_call"):
            continue
        identifier = item.get("id")
        key = identifier if isinstance(identifier, str) and identifier else ("unknown", index)
        merged = attempts.setdefault(key, {})
        merged.update({k: v for k, v in item.items() if v is not None})
    seen = set()
    repeats = 0
    for item in attempts.values():
        if item["type"] == "command_execution":
            command = item.get("command")
            if not isinstance(command, str) or not command.strip():
                continue
            signature = ("command_execution", command)
        else:
            tool, arguments = item.get("tool"), item.get("arguments")
            if not isinstance(tool, str) or not tool or arguments is None:
                continue
            if isinstance(arguments, str):
                try:
                    arguments = json.loads(arguments)
                except ValueError:
                    pass
            signature = ("mcp_tool_call", tool, json.dumps(
                arguments, sort_keys=True, ensure_ascii=False))
        if signature in seen:
            repeats += 1
        seen.add(signature)
    return (sum(item.get("type") == "command_execution"
                for item in attempts.values()), repeats)


def evaluate(o,case,mode,rep):
    directory=o.output/"runs"/f"{case}-{mode}-{rep}"
    record=directory/"outcome.json"
    retry_count=0
    if o.resume and record.exists():
        old=json.loads(record.read_text())
        if old["completed"] or not o.retry_incomplete:return old
        archive=o.output/"incomplete-attempts"/directory.name
        archive.parent.mkdir(parents=True,exist_ok=True)
        if archive.exists():raise RuntimeError("Archived trial exists: "+str(archive))
        retry_count=int(old.get("retry_count",0))+1
        directory.rename(archive)
    if directory.exists():raise RuntimeError("Refusing to overwrite trial "+str(directory))
    directory.mkdir(parents=True)
    work=directory/"checkout"
    prepare(o.source,work)
    cmd=[str(o.node),str(o.codex),"exec","--ignore-user-config","--ephemeral",
         "--json","--skip-git-repo-check","--sandbox","workspace-write",
         "--cd",str(work),"--model",o.model,"--config",
         'model_reasoning_effort="'+o.effort+'"']
    if mode=="mcp":
        bins=os.pathsep.join([str(o.node.parent),str(o.lsp_bin),
                              os.environ.get("PATH", "/usr/bin:/bin")])
        cmd+=["-c",'mcp_servers.simple-lsp.command="'+str(o.mcp)+'"',
              "-c",'mcp_servers.simple-lsp.args=["--workspace","'+str(work)+'","--tool-profile","coding","--result-format","text"]',
              "-c",'mcp_servers.simple-lsp.env.PATH="'+bins+'"']
        if o.tsserver:
            cmd+=["-c",'mcp_servers.simple-lsp.env.SIMPLE_LSP_TSSERVER_PATH="'+o.tsserver+'"']
    cmd.append(TASKS[case]+" "+SUFFIX)
    env=os.environ.copy()
    env["PATH"]=os.pathsep.join([str(o.node.parent),str(o.lsp_bin),env.get("PATH","")])
    start=time.monotonic()
    try:
        p=call(cmd,cwd=work,env=env,input="",timeout=o.timeout)
        stdout,stderr,exitcode=p.stdout,p.stderr,p.returncode
    except subprocess.TimeoutExpired as e:
        stdout=e.stdout or b""
        stderr=e.stderr or b""
        if isinstance(stdout,bytes):stdout=stdout.decode(errors="replace")
        if isinstance(stderr,bytes):stderr=stderr.decode(errors="replace")
        exitcode=124
    elapsed=round(time.monotonic()-start,2)
    (directory/"trace.jsonl").write_text(stdout)
    (directory/"stderr.txt").write_text(stderr)
    events=[]
    for line in stdout.splitlines():
        try:events.append(json.loads(line))
        except ValueError:pass
    finished=[e for e in events if e.get("type")=="turn.completed"]
    bad=any(e.get("type")=="turn.failed" for e in events)
    mcp,mcpok=trace_mcp_calls(events)
    command_calls,repeated_tool_calls=trace_tool_attempts(events)
    use=finished[-1].get("usage",{}) if finished else {}
    total_input=use.get("input_tokens")
    cached_input=use.get("cached_input_tokens")
    uncached_input=(total_input-cached_input
                    if type(total_input) is int and type(cached_input) is int
                    and 0 <= cached_input <= total_input else None)
    row=dict(study="coding-required",case=case,mode=mode,repeat=rep,
             retry_count=retry_count,seconds=elapsed,exitcode=exitcode,
             completed=exitcode==0 and bool(finished) and not bad,
             input_tokens=total_input,cached_tokens=cached_input,
             uncached_input_tokens=uncached_input,
             output_tokens=use.get("output_tokens"),reasoning_tokens=use.get("reasoning_output_tokens"),
             mcp_calls=len(mcp),valid_mcp_calls=len(mcpok),
             model=o.model,effort=o.effort,agent_version=o.agent_version,
             mcp_binary_sha=o.mcp_binary_sha,
             source_commit=SHA,policy_sha=hashlib.sha256(POLICY.encode()).hexdigest(),
             tasks_sha=hashlib.sha256((BASE/"docs/benchmarks/fastapi-template-tasks.json").read_bytes()).hexdigest(),
             grader_sha=hashlib.sha256(GRADER.read_bytes()).hexdigest(),
             mcp_tools=[x.get("tool") for x in mcp],
             command_calls=command_calls,repeated_tool_calls=repeated_tool_calls,
             graded=False,accepted=False,grader=None,
             changed_files=call(["git","diff","--name-only"],cwd=work).stdout.splitlines(),
             workdir=str(work))
    record.write_text(json.dumps(row,indent=2)+"\n")
    print("AGENT",case,mode,rep,"finished",row["completed"],"seconds",elapsed,
          "mcp",len(mcp),"valid_mcp",len(mcpok),"tokens",use.get("input_tokens"),flush=True)
    return row

def grade(o,records):
    for r in records:
        if r["graded"] or not r["completed"]:continue
        root=Path(r["workdir"]);env=os.environ.copy()
        if r["case"]=="summary":
            if not o.postgres_url:continue
            env["BENCH_POSTGRES_URL"]=o.postgres_url
            env["BENCH_ALLOW_DISPOSABLE_RESET"]="YES"
        try:
            p=call([str(o.python),str(GRADER),str(root),r["case"]],
                   cwd=root,env=env,timeout=80)
        except subprocess.TimeoutExpired:
            # A stalled independent grader is a failed acceptance trial, not
            # a reason to lose the whole paired study and its CSV evidence.
            p=subprocess.CompletedProcess([],124,"","Independent grader timed out after 80 seconds")
        try:judge=json.loads(p.stdout.splitlines()[-1])
        except (ValueError,IndexError):judge={"success":False,"error":(p.stderr+p.stdout)[-1000:]}
        if not isinstance(judge,dict):
            judge={"success":False,"error":"Independent grader must return a JSON object"}
        try:
            ok_syntax=call([str(o.python),"-m","compileall","-q",str(root/"backend/app")],cwd=root,timeout=60).returncode==0
        except subprocess.TimeoutExpired:
            ok_syntax=False
        ok_diff=call(["git","diff","--check"],cwd=root).returncode==0
        r["grader"]=judge;r["graded"]=True
        r["grader_exitcode"]=p.returncode
        r["syntax_ok"]=ok_syntax;r["diff_check_ok"]=ok_diff
        # Truthy strings such as "false" must never pass independent grading.
        r["accepted"]=bool(p.returncode==0 and judge.get("success") is True and ok_syntax and ok_diff)
        (root.parent/"outcome.json").write_text(json.dumps(r,indent=2)+"\n")
        print("GRADE",r["case"],r["mode"],r["repeat"],r["accepted"],flush=True)

def report(o,rows):
    fields=("study","case","mode","repeat","retry_count","seconds","exitcode","completed","graded","accepted",
            "input_tokens","cached_tokens","uncached_input_tokens","output_tokens","reasoning_tokens",
            "mcp_calls","valid_mcp_calls","command_calls","repeated_tool_calls","grader_exitcode",
            "syntax_ok","diff_check_ok",
            "model","effort","agent_version","mcp_binary_sha","source_commit","policy_sha","tasks_sha","grader_sha")
    with (o.output/"results.csv").open("w",newline="") as f:
        w=csv.DictWriter(f,fieldnames=fields);w.writeheader()
        for r in sorted(rows,key=lambda x:(x["case"],x["mode"],x["repeat"])):
            w.writerow({k:r.get(k) for k in fields})
    lines=["# Codex required-LSP coding profile A/B report","",
           "Model: "+o.model+"; effort: "+o.effort,
           "Both arms share identical conditional AGENTS.md; treatment may select edit-context LSP tool, baseline uses normal tools.",
           "Source pinned to "+SHA+".",
           "Dollar costs cannot be inferred from Codex CLI token usage.",
           "",
           "| Task | Arm | Completed | Accepted | Time (s) | Input total | Input cached | Input fresh | Output | Reasoning | Commands | Repeats | MCP valid/attempted |",
           "| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |"]
    for case in o.cases:
        for mode in ("baseline","mcp"):
            ar=[r for r in rows if r["case"]==case and r["mode"]==mode]
            done=[r for r in ar if r["completed"] and r["graded"]]
            avg=lambda key:round(statistics.mean(r[key] for r in done),1) if done and all(r.get(key)!=None for r in done) else "n/a"
            lines.append("| "+case+" | "+mode+" | "+str(len(done))+"/"+str(len(ar))+
                         " | "+str(sum(r["accepted"] for r in done))+"/"+str(len(done))+
                         " | "+str(avg("seconds"))+" | "+str(avg("input_tokens"))+
                         " | "+str(avg("cached_tokens"))+" | "+str(avg("uncached_input_tokens"))+
                         " | "+str(avg("output_tokens"))+" | "+str(avg("reasoning_tokens"))+
                         " | "+str(avg("command_calls"))+" | "+str(avg("repeated_tool_calls"))+
                         " | "+str(sum(r.get("valid_mcp_calls", 0) for r in ar))+
                         "/"+str(sum(r["mcp_calls"] for r in ar))+" |")
    lines+=["","Only completed, independently graded and genuinely MCP-using trials support conclusions.",""]
    (o.output/"report.md").write_text("\n".join(lines))
    print("REPORT",o.output/"report.md",flush=True)

def main():
    p=argparse.ArgumentParser(description=__doc__)
    for k in ("source","mcp","python","output","node","codex","lsp-bin"):
        p.add_argument("--"+k,required=True,type=Path)
    p.add_argument("--model",default="gpt-5.6-sol")
    p.add_argument("--effort",default="low")
    p.add_argument("--tsserver",default="")
    p.add_argument("--cases",nargs="+",choices=tuple(TASKS),default=list(TASKS))
    p.add_argument("--repeats",type=int,default=3)
    p.add_argument("--parallel",type=int,default=2)
    p.add_argument("--timeout",type=int,default=360)
    p.add_argument("--postgres-url",default="")
    p.add_argument("--resume",action="store_true")
    p.add_argument("--retry-incomplete",action="store_true")
    p.add_argument("--rescore-only",action="store_true")
    p.add_argument("--plan",action="store_true")
    o=p.parse_args()
    for k in ("source","mcp","output","node","codex","lsp_bin"):
        setattr(o,k,getattr(o,k).resolve())
    o.python=Path(os.path.abspath(o.python)) # keep virtualenv symlink
    actual=call(["git","rev-parse","HEAD"],cwd=o.source,check=True).stdout.strip()
    if actual!=SHA:p.error("Pinned template required: "+actual)
    version=call([str(o.node),str(o.codex),"--version"],timeout=20)
    if version.returncode or not version.stdout.strip():
        p.error("Cannot verify Codex CLI version: "+version.stderr)
    o.agent_version=version.stdout.strip()
    if not o.mcp.is_file():p.error("MCP executable not found: "+str(o.mcp))
    o.mcp_binary_sha=hashlib.sha256(o.mcp.read_bytes()).hexdigest()
    if o.repeats<1 or o.parallel<1:p.error("Invalid repeats/parallel")
    if o.postgres_url and not ("@127.0.0.1:" in o.postgres_url or "@localhost:" in o.postgres_url):
        p.error("Postgres must be on disposable localhost bench DB")
    planned=[(c,m,i) for c in o.cases for i in range(1,o.repeats+1)
             for m in (("baseline","mcp") if i%2 else ("mcp","baseline"))]
    print("PLAN",len(planned),"model",o.model,"cases",o.cases,flush=True)
    if o.plan:return
    o.output.mkdir(exist_ok=True,parents=True)
    ag=o.output/"AGENTS.md"
    if ag.exists() and ag.read_text()!=POLICY:raise RuntimeError("Policy changed; use fresh output")
    ag.write_text(POLICY)
    if o.rescore_only:
        rows=[]
        for c,m,i in planned:
            rec=o.output/"runs"/f"{c}-{m}-{i}"/"outcome.json"
            if not rec.exists():continue
            r=json.loads(rec.read_text());r["graded"]=False;r["accepted"]=False;rows.append(r)
    else:
        with ThreadPoolExecutor(max_workers=o.parallel) as pool:
            futures=[pool.submit(evaluate,o,*entry) for entry in planned]
            rows=[f.result() for f in as_completed(futures)]
    grade(o,rows)
    report(o,rows)

if __name__=="__main__":main()
