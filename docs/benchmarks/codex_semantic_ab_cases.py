#!/usr/bin/env python3
"""Task-oriented, independently checkable semantic development investigations.

These use a real Go project, not tiny mocked LSP API fixtures. Agents must
reason about actual source ownership and cross-package relationships.
"""
CASES = [
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
        assert len(case["answer_must_include"])>1
