"""Regression: independently seeded A/B runs must share a stable Git hash."""
import importlib.util
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

SCRIPT = Path(__file__).with_name('run_codex_product_ab.py')
spec = importlib.util.spec_from_file_location('product_ab_seed_test', SCRIPT)
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class DeterministicSeedTest(unittest.TestCase):
    def test_same_fixture_same_commit_across_independent_runs(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            frozen = root / 'frozen-sources'
            (frozen / 'go').mkdir(parents=True)
            (frozen / 'go' / 'go.mod').write_text('module example.com/fixture\n\ngo 1.22\n')
            (frozen / 'go' / 'sample.go').write_text('package fixture\n')
            case = {'id': 'regression', 'seed': 'go', 'edits': []}
            with patch.object(runner, 'FROZEN', frozen):
                first = runner.make_baseline(case, root / 'first')
                second = runner.make_baseline(case, root / 'second')
            self.assertEqual(first, second)


if __name__ == '__main__':
    unittest.main()
