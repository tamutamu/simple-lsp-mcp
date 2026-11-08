import tempfile
import unittest
from pathlib import Path

from semantic_bench_targets import resolve_go_target


class GoTargetPositionTests(unittest.TestCase):
    def test_method_selection_is_one_based(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "api.go").write_text(
                "// a comment mentioning Track does not count\n"
                "func (s *Store) Track(ctx context.Context) error {\n"
                "    return nil\n"
                "}\n"
            )
            result = resolve_go_target(root, {
                "path": "api.go", "symbol_path": "Track",
            })
            self.assertEqual(result["line"], 2)
            self.assertEqual(result["column"], 17)
    def test_ambiguous_declaration_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "api.go").write_text(
                "func (s *Store) Track(){}\n"
                "func (t *Tracer) Track(){}\n"
            )
            with self.assertRaises(ValueError):
                resolve_go_target(root, {
                    "path": "api.go", "symbol_path": "Track",
                })


if __name__ == "__main__":
    unittest.main()
