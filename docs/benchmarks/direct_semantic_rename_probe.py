#!/usr/bin/env python3
"""Exercise actual one-shot MCP semantic rename on a disposable pinned FastAPI tree."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import select
import subprocess
import time

def command(cmd, cwd, stdin=None):
    return subprocess.run(cmd, cwd=cwd, input=stdin, capture_output=True, check=True)

class Client:
    def __init__(self, binary, root, profile="coding"):
        self.counter = 0
        self.proc = subprocess.Popen(
            [binary, "--workspace", root, "--tool-profile", profile, "--result-format", "text"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        )
        self.request("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                "clientInfo": {"name": "rename-actual-probe", "version": "1"}})
        self.proc.stdin.write(b'{"jsonrpc":"2.0","method":"notifications/initialized"}\n')
        self.proc.stdin.flush()

    def request(self, method, params):
        self.counter += 1
        start = time.perf_counter()
        self.proc.stdin.write((json.dumps({"jsonrpc":"2.0","id":self.counter,"method":method,"params":params})+"\n").encode())
        self.proc.stdin.flush()
        for _ in range(120):
            if not select.select([self.proc.stdout], [], [], 1.0)[0]:
                continue
            line = self.proc.stdout.readline()
            if not line:
                raise RuntimeError("MCP process exited unexpectedly")
            response = json.loads(line)
            if response.get("id") != self.counter:
                continue
            if response.get("error"):
                raise RuntimeError(str(response["error"]))
            result = response["result"]
            if method != "tools/call":
                return result, (time.perf_counter()-start)*1000, len(line)
            if result.get("isError"):
                raise RuntimeError(str(result.get("content")))
            payload = result.get("structuredContent")
            if payload is None:
                payload = json.loads(result["content"][0]["text"])
            if isinstance(payload,dict) and "error" in payload:
                raise RuntimeError(str(payload["error"]))
            return payload, (time.perf_counter()-start)*1000, len(line)
        raise TimeoutError(method)

    def close(self):
        self.proc.terminate()
        try: self.proc.wait(timeout=3)
        except subprocess.TimeoutExpired: self.proc.kill()

def main():
    p=argparse.ArgumentParser()
    p.add_argument("--source", required=True)
    p.add_argument("--work", required=True)
    p.add_argument("--binary", required=True)
    p.add_argument("--path", default="backend/app/crud.py")
    p.add_argument("--symbol", default="get_user_by_email")
    p.add_argument("--new-name", default="find_user_by_email")
    p.add_argument("--one-call", action="store_true", help="Skip read-only preview; use one write tool")
    p.add_argument("--mcp-profile", choices=("coding","rename"), default="coding")
    args=p.parse_args()
    src=Path(args.source).resolve()
    root=Path(args.work).resolve()
    if root.exists():raise SystemExit("Refusing to overwrite "+str(root))
    root.mkdir(parents=True)
    archive=subprocess.Popen(["git","archive","HEAD"],cwd=src,stdout=subprocess.PIPE)
    command(["tar","-x","-C",str(root)],cwd=src,stdin=archive.stdout.read())
    archive.stdout.close()
    if archive.wait():raise SystemExit("git archive failed")
    for step in (["git","init","-q"],["git","add","-A"],
                 ["git","-c","user.name=Benchmark","-c","user.email=benchmark@example.invalid","commit","-qm","baseline"]):
        command(step,cwd=root)
    client=Client(args.binary,str(root),args.mcp_profile)
    try:
        params={"path":args.path,"symbol_path":args.symbol,"new_name":args.new_name}
        names,_,_=client.request("tools/list",{})
        offered={d["name"] for d in names["tools"]}
        if not {"preview_rename","rename_symbol"}.issubset(offered):
            raise RuntimeError("Coding profile does not advertise semantic rename")
        if args.one_call:
            prev_ms,prev_bytes=0,0
        else:
            preview,prev_ms,prev_bytes=client.request("tools/call",{"name":"preview_rename","arguments":params})
            if preview.get("file_count",0)<1 or preview.get("edit_count",0)<1:
                raise RuntimeError("LSP returned no rename edits")
        applied,apply_ms,apply_bytes=client.request("tools/call",{"name":"rename_symbol",
                                                                "arguments":dict(params,apply=True)})
        if not applied.get("applied"):
            raise RuntimeError("LSP did not confirm applied rename")
        diff=command(["git","diff","--binary"],cwd=root).stdout
        paths=command(["git","diff","--name-only"],cwd=root).stdout.decode().splitlines()
        remaining=[]
        for tree in ("backend/app","backend/tests"):
            for f in (root/tree).rglob("*.py"):
                if args.symbol in f.read_text(encoding="utf-8"):
                    remaining.append(str(f.relative_to(root)))
        check=command(["git","diff","--check"],cwd=root)
        if remaining: raise RuntimeError("Old identifier still exists: "+str(remaining))
        if applied.get("file_count")!=len(paths):
            raise RuntimeError(f"Mismatch: {applied.get('file_count')} LSP files vs {len(paths)} git files")
        print(json.dumps({
            "mode":"semantic_rename_direct","preview_ms":round(prev_ms,3),
            "apply_ms":round(apply_ms,3),"preview_response_bytes":prev_bytes,
            "apply_response_bytes":apply_bytes,
            "file_count":applied.get("file_count"),"edit_count":applied.get("edit_count"),
            "changed_files":paths,"diff_sha256":hashlib.sha256(diff).hexdigest(),
            "git_diff_check":check.returncode==0,"leftover_old_name":remaining
        },ensure_ascii=False,indent=2))
    finally:client.close()

if __name__=="__main__":main()