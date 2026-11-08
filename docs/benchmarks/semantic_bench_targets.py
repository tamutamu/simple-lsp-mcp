"""Locate exact Go declarations in a pinned benchmark tree.

Never guess or hard-code declaration coordinates: a wrong line/column can make
gopls appear to miss references independently of any MCP advantage.
"""
from pathlib import Path
import re

GO_DECL = re.compile(
    r"^[ \t]*func[ \t]+(?:\([^\r\n)]*\)[ \t]+)?(?P<name>[A-Za-z_]\w*)[ \t]*\(",
    re.MULTILINE,
)

def resolve_go_target(root: Path, target: dict) -> dict:
    source = root / target["path"]
    if source.suffix != ".go" or not source.is_file():
        raise ValueError(f"not a Go source file: {source}")
    text = source.read_text(encoding="utf-8")
    matches = [m for m in GO_DECL.finditer(text)
               if m.group("name") == target["symbol_path"]]
    if len(matches) != 1:
        raise ValueError(
            f"ambiguous/missing declaration {target['symbol_path']} "
            f"in {source}: {len(matches)} matches"
        )
    start = matches[0].start("name")
    line = text.count("\n", 0, start) + 1
    column = start - text.rfind("\n", 0, start)
    return {**target, "line": line, "column": column}
