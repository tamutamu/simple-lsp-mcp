#!/usr/bin/env python3
"""Task-oriented, independently checkable semantic development investigations.

These use a real Go project, not tiny mocked LSP API fixtures. Agents must
reason about actual source ownership and cross-package relationships.
"""
CASES = [
    {
        "id":"S02","name":"go-exhaustive-semantic-constructor-impact",
        "language":"go","seed":"go","read_only":True,
        "prompt":(
            "We are evaluating the impact of renaming the exact constructor New "
            "declared in internal/tools/engine.go. Identify EVERY Go source file "
            "that calls that declaration, including unqualified calls within "
            "the tools package and tests, and qualified calls in other packages. "
            "Do NOT include references to unrelated symbols also named New, "
            "declarations, imports, comments or literal examples. Do not edit "
            "the source. Return ONLY a JSON object with one key named files, "
            "whose value is the sorted array of unique workspace-relative "
            "file paths with actual calls. No prose or Markdown fences."
        ),
        "answer_exact_files":['cmd/simple-lsp-mcp/doctor.go', 'cmd/simple-lsp-mcp/main.go', 'internal/tools/auto_config_real_test.go', 'internal/tools/diagnostics_integrity_test.go', 'internal/tools/document_flat_integrity_test.go', 'internal/tools/edit_assembly_test.go', 'internal/tools/edit_real_test.go', 'internal/tools/edit_test.go', 'internal/tools/engine_test.go', 'internal/tools/impact_real_test.go', 'internal/tools/location_integrity_test.go', 'internal/tools/real_lsp_test.go', 'internal/tools/reference_filter_real_test.go', 'internal/tools/relation_preview_test.go', 'internal/tools/workspace_confinement_test.go', 'internal/tools/workspace_list_test.go']
    },
    {
        "id":"S01","name":"go-interface-implementation-audit",
        "language":"go","seed":"go","read_only":True,
        "prompt":(
            "You are preparing a breaking change to the document.Notifier interface "
            "in internal/document/store.go. Identify every concrete Go type in this "
            "repository that implements this interface, including test doubles, "
            "and report exact source paths, type names, and the method/signature that "
            "satisfies the contract. Distinguish the interface declaration from its "
            "implementations. Do not edit any files. Include tests and production code."
        ),
        "answer_must_include":[
            "internal/document/store.go","Notifier",
            "internal/document/store_test.go","fakeNotifier",
            "internal/lsp/session/session.go","Session",
            "internal/lsp/transport/transport.go","Transport",
            "Notify"
        ],
    },
]


def validate() -> None:
    assert len({case["id"] for case in CASES})==len(CASES)
    for case in CASES:
        assert case["read_only"] and case["seed"] in {"go","ts","py"}
        if "answer_exact_files" in case:
            assert len(case["answer_exact_files"])>1
        else:
            assert len(case["answer_must_include"])>1
