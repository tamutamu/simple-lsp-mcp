"""The A/B runner must reject stale MCP binaries before any model trial."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


RUNNER = Path(__file__).with_name("run_codex_product_ab.py")


class MCPProfilePreflightTest(unittest.TestCase):
    def test_stale_binary_is_rejected_before_frozen_source_creation(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            stale = root / "stale-mcp"
            stale.write_text("#!/bin/sh\necho 'invalid --tool-profile: expected full or coding' >&2\nexit 2\n")
            stale.chmod(0o755)
            home = root / "codex-home"
            home.mkdir()
            output = root / "output"
            env = os.environ.copy()
            env.update({
                "PRODUCT_AB_MCP": str(stale),
                "PRODUCT_AB_CODEX": "/bin/true",
                "PRODUCT_AB_GO": "/bin/true",
                "PRODUCT_AB_CODEX_HOME": str(home),
                "PRODUCT_AB_OUTPUT": str(output),
                "PRODUCT_AB_PROFILE": "analysis",
            })
            run = subprocess.run(
                ["python3", str(RUNNER), "--cases", "S02", "--repetitions", "1"],
                env=env, capture_output=True, text=True, timeout=20,
            )
            self.assertNotEqual(run.returncode, 0)
            self.assertIn("does not support profile 'analysis'", run.stderr)
            self.assertFalse((output / "frozen-sources").exists())


if __name__ == "__main__":
    unittest.main()
