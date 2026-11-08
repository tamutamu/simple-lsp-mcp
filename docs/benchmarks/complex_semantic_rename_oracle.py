#!/usr/bin/env python3
"""Independent, isolated real-Go-LSP oracle for a complex interface rename.

Creates a fresh git-archive checkout, applies one semantic WorkspaceEdit, saves
the exact diff and a compact report for comparing complete agent work.
"""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess

from direct_semantic_rename_probe import Client

def run(cmd, cwd, input_bytes=None):
    return subprocess.run(cmd,cwd=cwd,input=input_bytes,capture_output=True,check=True)

def main():
    p=argparse.ArgumentParser()
    p.add_argument("--source",required=True,type=Path)
    p.add_argument("--binary",required=True,type=Path)
    p.add_argument("--work",required=True,type=Path)
    p.add_argument("--output",required=True,type=Path)
    p.add_argument("--path",default="lib/events/api.go")
    p.add_argument("--symbol",default="IAuditLog/EmitAuditEvent")
    p.add_argument("--new-name",default="RecordAuditEvent")
    o=p.parse_args()
    source=o.source.resolve()
    dst=o.work.resolve()
    if dst.exists():p.error("work dir exists; refusing to overwrite")
    dst.mkdir(parents=True)
    archive=run(["git","archive","HEAD"],source).stdout
    run(["tar","-x","-C",str(dst)],source,archive)
    for cmd in (
        ["git","init","-q"],["git","add","-A"],
        ["git","-c","user.name=Benchmark","-c","user.email=benchmark@example.invalid","commit","-qm","base"],
    ):
        run(cmd,dst)
    client=Client(str(o.binary.resolve()),str(dst),"coding",90)
    try:
        result,ms,response_size=client.request("tools/call",{"name":"rename_symbol","arguments":{
            "path":o.path,"symbol_path":o.symbol,"new_name":o.new_name,"apply":True,
        }})
        if result.get("applied") is not True:
            raise RuntimeError("LSP did not report success: "+str(result)[:500])
        diff=run(["git","diff","--binary"],dst).stdout
        run(["git","diff","--check"],dst)
        paths=run(["git","diff","--name-only"],dst).stdout.decode().splitlines()
        if result.get("file_count")!=len(paths):
            raise RuntimeError("file count does not match git diff")
        expected={"source_commit":run(["git","rev-parse","HEAD"],source).stdout.decode().strip(),
            "rename_path":o.path,"symbol_path":o.symbol,"new_name":o.new_name,
            "elapsed_ms":round(ms,3),"response_bytes":response_size,
            "file_count":result["file_count"],"edit_count":result["edit_count"],
            "changed_files":paths,"sha256":hashlib.sha256(diff).hexdigest(),
            "complete_edit_applied":True,
        }
        o.output.write_text(json.dumps(expected,ensure_ascii=False,indent=2))
        (o.output.parent/(o.output.stem+".patch")).write_bytes(diff)
        print(json.dumps(expected,ensure_ascii=False,indent=2),flush=True)
    finally:
        client.close()

if __name__=="__main__":
    main()
