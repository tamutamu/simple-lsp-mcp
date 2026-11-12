"""Independent preflight and grading regression checks for A/B fixtures."""
from __future__ import annotations

import importlib.util
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


HERE = Path(__file__).resolve().parent


def load(filename: str, name: str):
    spec = importlib.util.spec_from_file_location(name, HERE / filename)
    assert spec is not None and spec.loader is not None
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


CATALOG = load("codex_product_ab_cases.py", "ab_fixture_catalog")
RUNNER = load("run_codex_product_ab.py", "ab_fixture_runner")


def make_fixture(files: dict[str, str], directory: Path) -> None:
    for name, data in files.items():
        path = directory / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(data, encoding="utf-8")


def run_typecheck(directory: Path, command: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(command, cwd=directory, text=True, capture_output=True, timeout=60)


class FixturePreflightTests(unittest.TestCase):
    @unittest.skipUnless(shutil.which("pyright") and shutil.which("node"), "Pyright and Node required")
    def test_python_fixture_clean_and_seeding_error_specific(self):
        with tempfile.TemporaryDirectory(prefix="ab-pyright-test-") as tmp:
            root = Path(tmp)
            make_fixture(CATALOG.PY_BASE, root)
            command = [shutil.which("pyright"), "-p", "backend/pyrightconfig.bench.json"]
            clean = run_typecheck(root, command)
            self.assertEqual(clean.returncode, 0, clean.stdout + clean.stderr)
            path = root / "backend/benchdomain/pricing.py"
            broken = next(x for x in CATALOG.CASES if x["id"] == "T39")["edits"][0]
            relative, before, after = broken.split("|", 2)
            self.assertEqual(relative, "backend/benchdomain/pricing.py")
            self.assertEqual(path.read_text().count(before), 1)
            path.write_text(path.read_text().replace(before, after, 1))
            failed = run_typecheck(root, command)
            self.assertNotEqual(failed.returncode, 0, "type error not detected")
            self.assertIn("pricing.py", failed.stdout)
            self.assertNotIn("reportUnusedImport", failed.stdout,
                             "clean fixture has unrelated diagnostics")

    @unittest.skipUnless(shutil.which("tsc") and shutil.which("node"), "TypeScript checker required")
    def test_typescript_fixture_clean_and_seeding_error_specific(self):
        with tempfile.TemporaryDirectory(prefix="ab-typescript-test-") as tmp:
            root = Path(tmp)
            make_fixture(CATALOG.TS_BASE, root)
            command = [shutil.which("node"), shutil.which("tsc"), "-p", "frontend/tsconfig.bench.json"]
            clean = run_typecheck(root, command)
            self.assertEqual(clean.returncode, 0, clean.stdout + clean.stderr)
            broken = next(x for x in CATALOG.CASES if x["id"] == "T37")["edits"][0]
            relative, before, after = broken.split("|", 2)
            path = root / relative
            self.assertEqual(path.read_text().count(before), 1)
            path.write_text(path.read_text().replace(before, after, 1))
            failed = run_typecheck(root, command)
            self.assertNotEqual(failed.returncode, 0, "type error not detected")
            self.assertIn("billing.ts", failed.stdout)

    def test_frozen_snapshot_hash_is_deterministic_and_content_sensitive(self):
        with tempfile.TemporaryDirectory(prefix="ab-hash-") as tmp:
            left, right = Path(tmp) / "left", Path(tmp) / "right"
            for path in (left, right):
                (path / "sub").mkdir(parents=True)
            (left / "sub" / "b.go").write_text("package sub\n")
            (left / "a.py").write_text("print(1)\n")
            (right / "a.py").write_text("print(1)\n")
            (right / "sub" / "b.go").write_text("package sub\n")
            h = RUNNER.snapshot_sha256(left)
            self.assertEqual(h, RUNNER.snapshot_sha256(right))
            self.assertEqual(len(h), 64)
            (right / "a.py").write_text("print(2)\n")
            self.assertNotEqual(h, RUNNER.snapshot_sha256(right))
            (left / "sub" / "shortcut").symlink_to("b.go")
            self.assertNotEqual(h, RUNNER.snapshot_sha256(left))
            (left / "sub" / "shortcut").unlink()
            self.assertEqual(h, RUNNER.snapshot_sha256(left))

    def test_quota_error_stops_future_trials_without_fabricating_failure(self):
        import json
        with tempfile.TemporaryDirectory(prefix="ab-quota-") as tmp:
            path = Path(tmp) / "trace.jsonl"
            items=[
                {"event":{"method":"turn/completed","params":{"turn":{
                    "status":"failed","error":{"codexErrorInfo":"usageLimitExceeded"}
                }}}},
            ]
            path.write_text("\n".join(json.dumps(x) for x in items)+"\n")
            self.assertTrue(RUNNER.trace_has_quota_error(path))
            items=[{"event":{"method":"turn/completed","params":{"turn":{
                "status":"failed","error":{"codexErrorInfo":"modelError"}
            }}}}]
            path.write_text("\n".join(json.dumps(x) for x in items)+"\n")
            self.assertFalse(RUNNER.trace_has_quota_error(path))
            items=[{"event":{"method":"error","params":{"error":{
                "codexErrorInfo":"usageLimitExceeded"
            }}}}]
            path.write_text("\n".join(json.dumps(x) for x in items)+"\n")
            self.assertTrue(RUNNER.trace_has_quota_error(path))

    def test_readonly_grader_catches_untracked_source_and_ignores_python_caches(self):
        with tempfile.TemporaryDirectory(prefix="ab-changes-") as tmp:
            root = Path(tmp)
            def git(*args: str):
                p = subprocess.run(["git", *args], cwd=root, text=True,
                                   capture_output=True, timeout=20)
                self.assertEqual(p.returncode, 0, p.stderr)
                return p.stdout
            git("init", "-q")
            (root / "tracked.go").write_text("package main\n")
            git("add", "-A")
            git("-c", "user.name=Benchmark", "-c", "user.email=bench@example.invalid",
                "commit", "-qm", "seed")
            (root / "tracked.go").write_text("package main\n// modified\n")
            (root / "untracked.go").write_text("package main\n")
            cache = root / "__pycache__"
            cache.mkdir()
            (cache / "thing.cpython-310.pyc").write_bytes(b"x")
            self.assertEqual(RUNNER.changed_files(root), ["tracked.go", "untracked.go"])
            git("add", "untracked.go")
            self.assertEqual(RUNNER.changed_files(root), ["tracked.go", "untracked.go"])


if __name__ == "__main__":
    unittest.main()
