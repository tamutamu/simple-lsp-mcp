#!/usr/bin/env python3
"""Build a six-declaration oracle using one persistent LSP process.

Three two-target phases all use the *same* MCP/LSP instance; an independent
single-target invocation cross-checks every file, count and completeness flag.
"""
import argparse
import hashlib
import json
from pathlib import Path
import statistics
import subprocess

from direct_semantic_rename_probe import Client
from semantic_bench_targets import resolve_go_target

SYMBOL_GROUPS = (
    ("EmitAuditEvent", "PostSessionSlice"),
    ("SearchEvents", "EmitAuditEventLegacy"),
    ("GetSessionEvents", "SearchSessionEvents"),
)
DEFAULT_PATH="lib/events/dynamoevents/dynamoevents.go"

def invoke(client, target):
    return client.request("tools/call", {"name": "investigate_change", "arguments": target})

def normalize(result):
    return {
        "symbol_path":result["target"]["name"].split(".")[-1],
        "affected_files":[f["path"] if isinstance(f,dict) else f
                          for f in result["affected_files"]],
        "affected_declarations": result["affected_declarations"],
        "direct_reference_count": result["direct_reference_count"],
        "incomplete": not result["meta"]["complete"],
    }

def create_oracle(src, binary, rel_path, groups=SYMBOL_GROUPS):
    src=src.resolve()
    binary=binary.resolve()
    phases=[]
    for index,names in enumerate(groups):
        phases.append({
            "index":index+1,
            "targets":[resolve_go_target(src, {"path":rel_path, "symbol_path":name})
                       for name in names],
        })
    client=Client(str(binary),str(src),"investigate",90)
    try:
        # All tool calls happen through this one persistent stdio connection.
        for phase in phases:
            req={"targets":[{"path":target["path"],"symbol_path":target["symbol_path"]}
                            for target in phase["targets"]],
                 "depth":2,"max_nodes":30,"detail":"union"}
            data,ms,size=invoke(client,req)
            if data["targets_count"]!=len(phase["targets"]):
                raise RuntimeError(f"phase {phase['index']} lost targets")
            phase["result"]=data
            phase["ms"]=round(ms,3)
            phase["reply_bytes"]=size
            for j,target in enumerate(phase["targets"]):
                single,_,_=invoke(client,{
                    "path":target["path"],"symbol_path":target["symbol_path"],
                    "depth":2,"max_nodes":30,"detail":"files",
                })
                batched=data["results"][j]
                assert batched["target"]==single["target"],target
                assert batched["direct_reference_count"]==single["direct_reference_count"],target
                assert batched["affected_declarations"]==single["affected_declarations"],target
                assert batched["meta"]==single["meta"],target
                assert batched["affected_files"]==[f["path"] for f in single["affected_files"]],target
            independent=set(path for result in data["results"]
                for path in result["affected_files"])
            assert set(data["affected_files_union"])==independent
    finally:
        client.close()
    return {
        "source_commit":subprocess.check_output(["git","rev-parse","HEAD"],cwd=src,text=True).strip(),
        "binary_sha256":hashlib.sha256(binary.read_bytes()).hexdigest(),
        "source_files":sum(p.suffix in (".go",".py",".ts",".tsx") for p in src.rglob("*") if p.is_file()),
        "persistent_mcp_session":True,
        "independently_verified":True,
        "phases":phases,
        "target_count":sum(len(phase["targets"]) for phase in phases),
        "total_reply_bytes":sum(phase["reply_bytes"] for phase in phases),
        "warm_phase_ms":statistics.median(phase["ms"] for phase in phases[1:]),
    }

def main():
    p=argparse.ArgumentParser()
    p.add_argument("--source",type=Path,required=True)
    p.add_argument("--binary",type=Path,required=True)
    p.add_argument("--output",type=Path,required=True)
    p.add_argument("--path",default=DEFAULT_PATH)
    o=p.parse_args()
    answer=create_oracle(o.source,o.binary,o.path)
    o.output.write_text(json.dumps(answer,indent=2,ensure_ascii=False))
    print(json.dumps({
        "source_files":answer["source_files"],"target_count":answer["target_count"],
        "total_reply_bytes":answer["total_reply_bytes"],"warm_phase_ms":answer["warm_phase_ms"],
        "phases":[{"index":p["index"],"names":[t["symbol_path"] for t in p["targets"]],
           "ms":p["ms"],"bytes":p["reply_bytes"],
           "affected_union":p["result"]["affected_file_count"]} for p in answer["phases"]],
        "independently_verified":answer["independently_verified"],
    },ensure_ascii=False,indent=2),flush=True)

if __name__=="__main__":
    main()
