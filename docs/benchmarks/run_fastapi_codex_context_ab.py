#!/usr/bin/env python3
"""Codex A/B: same pinned FastAPI source and identical AGENTS.md, MCP on/off."""
import argparse, csv, json, os, select, shutil, statistics, subprocess, time
from pathlib import Path
from concurrent.futures import ThreadPoolExecutor, as_completed

BASE=Path(__file__).resolve().parents[2]
TASKS=json.loads((BASE/"docs/benchmarks/fastapi-template-tasks.json").read_text())
GRADER=BASE/"docs/benchmarks/fastapi-template-grader.py"
SHA="b1125d8839482cb7bbae603ddc6d0081bf87532e"
POLICY="""# Codex source-context evaluation - identical in both arms
When simple-lsp MCP is connected, you MUST invoke a task-relevant simple-lsp
tool before editing. Choose the smallest response matching your current need:
- When the file and symbol are already known, call inspect_symbol(path=..., symbol_path=..., view="source") directly. No outline lookup needed.
- Otherwise find the target with normal file search or compact get_document_symbols / search_symbols.
- Need the declaration or function body: inspect_symbol(view="signature" or
  "source") on the exact symbol_id, symbol_path or source position.
- Need only an exact location: inspect_symbol(view="location").
- Need semantic usages with actual source: find_references(relation_view="lines").
- For a multi-file semantic rename, use rename_symbol when approval permits.
Do not call unrelated MCP tools or read an entire file again if the obtained
source snippet already contains all code needed for this task.
If the MCP is NOT connected, use ordinary search, reading, editing and tests.
Do not edit AGENTS.md or install dependencies. Run relevant checks.
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

def evaluate(o,case,mode,rep):
    directory=o.output/"runs"/f"{case}-{mode}-{rep}"
    record=directory/"outcome.json"
    if o.resume and record.exists():
        old=json.loads(record.read_text())
        if old["completed"] or not o.retry_incomplete:return old
        archive=o.output/"incomplete-attempts"/directory.name
        archive.parent.mkdir(parents=True,exist_ok=True)
        if archive.exists():raise RuntimeError("Archived trial exists: "+str(archive))
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
              "-c",'mcp_servers.simple-lsp.args=["--workspace","'+str(work)+'"]',
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
    items=[e["item"] for e in events if e.get("type")=="item.completed" and isinstance(e.get("item"),dict)]
    mcp=[i for i in items if i.get("type")=="mcp_tool_call" and i.get("server")=="simple-lsp"]
    mcpok=[i for i in mcp if i.get("status") == "completed" and not i.get("error") and not (i.get("result") or {}).get("structured_content",{}).get("error")]
    use=finished[-1].get("usage",{}) if finished else {}
    row=dict(case=case,mode=mode,repeat=rep,seconds=elapsed,exitcode=exitcode,
             completed=exitcode==0 and bool(finished) and not bad,
             input_tokens=use.get("input_tokens"),cached_tokens=use.get("cached_input_tokens"),
             output_tokens=use.get("output_tokens"),reasoning_tokens=use.get("reasoning_output_tokens"),
             mcp_calls=len(mcp),valid_mcp_calls=len(mcpok),
             mcp_tools=[x.get("tool") for x in mcp],
             command_calls=sum(i.get("type")=="command_execution" for i in items),
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
        p=call([str(o.python),str(GRADER),str(root),r["case"]],
               cwd=root,env=env,timeout=80)
        try:judge=json.loads(p.stdout.splitlines()[-1])
        except (ValueError,IndexError):judge={"success":False,"error":(p.stderr+p.stdout)[-1000:]}
        ok_syntax=call([str(o.python),"-m","compileall","-q",str(root/"backend/app")],cwd=root,timeout=60).returncode==0
        ok_diff=call(["git","diff","--check"],cwd=root).returncode==0
        r["grader"]=judge;r["graded"]=True
        r["syntax_ok"]=ok_syntax;r["diff_ok"]=ok_diff
        r["accepted"]=bool(judge.get("success") and ok_syntax and ok_diff)
        (root.parent/"outcome.json").write_text(json.dumps(r,indent=2)+"\n")
        print("GRADE",r["case"],r["mode"],r["repeat"],r["accepted"],flush=True)

def report(o,rows):
    fields=("case","mode","repeat","seconds","exitcode","completed","graded","accepted",
            "input_tokens","cached_tokens","output_tokens","reasoning_tokens",
            "mcp_calls","valid_mcp_calls","command_calls")
    with (o.output/"results.csv").open("w",newline="") as f:
        w=csv.DictWriter(f,fieldnames=fields);w.writeheader()
        for r in sorted(rows,key=lambda x:(x["case"],x["mode"],x["repeat"])):
            w.writerow({k:r.get(k) for k in fields})
    lines=["# Codex coding-context MCP A/B report","",
           "Model: "+o.model+"; effort: "+o.effort,
           "Both arms share the exact same AGENTS.md with context-aware, conditional MCP usage guidance.",
           "Source pinned to "+SHA+".",
           "Dollar costs cannot be inferred from Codex CLI token usage.",
           "",
           "| Task | Arm | Completed | Accepted | Mean time (s) | Mean input tokens | Mean output tokens | MCP calls |",
           "| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |"]
    for case in o.cases:
        for mode in ("baseline","mcp"):
            ar=[r for r in rows if r["case"]==case and r["mode"]==mode]
            done=[r for r in ar if r["completed"] and r["graded"]]
            avg=lambda key:round(statistics.mean(r[key] for r in done),1) if done and all(r.get(key)!=None for r in done) else "n/a"
            lines.append("| "+case+" | "+mode+" | "+str(len(done))+"/"+str(len(ar))+
                         " | "+str(sum(r["accepted"] for r in done))+"/"+str(len(done))+
                         " | "+str(avg("seconds"))+" | "+str(avg("input_tokens"))+
                         " | "+str(avg("output_tokens"))+" | "+str(sum(r["mcp_calls"] for r in ar))+" |")
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
