#!/usr/bin/env python3
"""Fixed, checkable task catalog for Codex-only and Codex+simple-lsp comparisons."""
from __future__ import annotations
import json
import os
from pathlib import Path

ROOT=Path(__file__).resolve().parents[2]
FASTAPI=Path(os.environ.get("PRODUCT_AB_FASTAPI", str(ROOT/".scratch/fastapi-bench-source"))).resolve()

GO_TYPE_FIX='internal/tools/engine.go|MaxResults:      cfg.MaxResults,|MaxResults:      cfg.RequestTimeout,'
GO_RUNTIME_FIX='internal/tools/rename.go|result["applied"] = true|result["applied"] = "true"'

TS_BASE={
 "frontend/tsconfig.bench.json":json.dumps({
  "compilerOptions":{"target":"ES2022","module":"NodeNext","moduleResolution":"NodeNext","strict":True,"noEmit":True,"allowImportingTsExtensions":True,"types":[]},
  "include":["src/bench/**/*.ts"]},indent=2)+"\n",
 "frontend/src/bench/types.ts":'''export interface LineItem { sku: string; quantity: number; unitPriceCents: number }
export interface InvoiceSummary { subtotalCents: number; taxCents: number; totalCents: number }
export interface InventoryPort {
  findBySku(sku: string): LineItem | undefined;
  list(): LineItem[];
}
export const STANDARD_TAX_BPS = 1000;
''',
 "frontend/src/bench/billing.ts":'''import type { LineItem, InvoiceSummary } from "./types.ts";
import { STANDARD_TAX_BPS } from "./types.ts";

export function lineTotalCents(item: LineItem): number {
  return item.unitPriceCents * item.quantity;
}
export function subtotalCents(lines: LineItem[]): number {
  return lines.reduce((sum, item) => sum + lineTotalCents(item), 0);
}
export function invoiceSummary(lines: LineItem[], taxBps = STANDARD_TAX_BPS): InvoiceSummary {
  const subtotal = subtotalCents(lines);
  const tax = Math.round(subtotal * taxBps / 10000);
  return { subtotalCents: subtotal, taxCents: tax, totalCents: subtotal + tax };
}
''',
 "frontend/src/bench/repository.ts":'''import type { InventoryPort, LineItem } from "./types.ts";
export class InMemoryInventory implements InventoryPort {
  private readonly items: LineItem[];
  constructor(items: LineItem[]) { this.items = items; }
  findBySku(sku: string): LineItem | undefined {
    return this.items.find((item) => item.sku === sku);
  }
  list(): LineItem[] {
    return [...this.items];
  }
}
''',
 "frontend/src/bench/checkout.ts":'''import type { InventoryPort } from "./types.ts";
import { invoiceSummary } from "./billing.ts";
export function checkout(store: InventoryPort, skus: string[]) {
  const lines = skus.map((sku) => store.findBySku(sku)).filter((x) => x !== undefined);
  return invoiceSummary(lines);
}
''',
 "frontend/src/bench/billing.test.mjs":'''import test from "node:test";
import assert from "node:assert/strict";
import { lineTotalCents, invoiceSummary } from "./billing.ts";
import { InMemoryInventory } from "./repository.ts";
import { checkout } from "./checkout.ts";
test("line totals use numeric cents", () => {
  assert.equal(lineTotalCents({sku:"x",quantity:3,unitPriceCents:125}),375);
});
test("tax rounds to nearest cent", () => {
  assert.deepEqual(invoiceSummary([{sku:"x",quantity:1,unitPriceCents:105}],50),
    {subtotalCents:105,taxCents:1,totalCents:106});
});
test("checkout uses inventory items", () => {
  const inventory = new InMemoryInventory([{sku:"p",quantity:2,unitPriceCents:99}]);
  assert.deepEqual(checkout(inventory,["p"]),{subtotalCents:198,taxCents:20,totalCents:218});
});
''',
}
PY_BASE={
 "backend/benchdomain/__init__.py":"",
 "backend/benchdomain/types.py":'''from dataclasses import dataclass
@dataclass(frozen=True)
class Order:
    id: str
    owner: str
    amount_cents: int
    description: str | None = None
@dataclass(frozen=True)
class Invoice:
    subtotal_cents: int
    discount_cents: int
    total_cents: int
''',
 "backend/benchdomain/pricing.py":'''from .types import Invoice, Order
def discount_cents(subtotal_cents: int, rate_basis_points: int) -> int:
    return (subtotal_cents * rate_basis_points + 5000) // 10000
def invoice(orders: list[Order], discount_basis_points: int = 0) -> Invoice:
    subtotal = sum(order.amount_cents for order in orders)
    discount = discount_cents(subtotal, discount_basis_points)
    return Invoice(subtotal, discount, subtotal - discount)
''',
 "backend/benchdomain/orders.py":'''from .types import Order
def visible_orders(orders: list[Order], owner: str, is_admin: bool = False) -> list[Order]:
    if is_admin:
        return list(orders)
    return [order for order in orders if order.owner == owner]
def has_description(order: Order) -> bool:
    return order.description is not None and bool(order.description.strip())
''',
 "backend/benchdomain/reporting.py":'''from .orders import visible_orders
from .pricing import invoice
from .types import Invoice, Order
def owner_invoice(orders: list[Order], owner: str, is_admin: bool = False) -> Invoice:
    return invoice(visible_orders(orders, owner, is_admin))
''',
 "backend/tests/benchdomain/__init__.py":"",
 "backend/tests/benchdomain/test_domain.py":'''import unittest
from benchdomain.types import Order
from benchdomain.orders import visible_orders, has_description
from benchdomain.pricing import invoice
class BenchDomainTests(unittest.TestCase):
    def setUp(self):
        self.orders = [Order("1","alice",105,"tea"),Order("2","bob",95,"   "),Order("3","alice",100,None)]
    def test_visibility(self):
        self.assertEqual([x.id for x in visible_orders(self.orders,"alice")],["1","3"])
        self.assertEqual(len(visible_orders(self.orders,"alice",True)),3)
    def test_blank_description(self):
        self.assertFalse(has_description(self.orders[1]))
        self.assertFalse(has_description(self.orders[2]))
    def test_invoice_rounding(self):
        result=invoice(self.orders,500)
        self.assertEqual((result.subtotal_cents,result.discount_cents,result.total_cents),(300,15,285))
if __name__=="__main__": unittest.main()
''',
 "backend/pyrightconfig.bench.json":json.dumps({"include":["benchdomain"],"typeCheckingMode":"strict","pythonVersion":"3.12"},indent=2)+"\n",
}

CASES=[
 {"id":"T33","name":"unknown-code-understanding","language":"go","seed":"go","read_only":True,
  "prompt":"Inspect this unfamiliar Go repository. Identify which file and function implements semantic LSP references, how it resolves its target, and which part formats the source response for an agent. Return the exact paths and function names with concise reasoning. Do not edit files."},
 {"id":"T34","name":"cross-file-impact-analysis","language":"go","seed":"go","read_only":True,
  "prompt":"Before changing the package tools constructor, find every Go file under cmd/ and internal/ that directly calls tools.New, excluding unrelated New from other packages. List the unique affected file paths and explain the distinction. Do not edit files."},
 {"id":"T35","name":"go-type-error-fix","language":"go","seed":"go","edits":[GO_TYPE_FIX],
  "prompt":"The Go source has a type error in its LSP engine constructor. Diagnose the problem and fix only its cause without changing any public behavior. Verify with go test ./... and go vet ./... ."},
 {"id":"T36","name":"go-runtime-regression-fix","language":"go","seed":"go","edits":[GO_RUNTIME_FIX],
  "prompt":"A regression changed the type of the semantic rename tool's applied result. It compiles, but clients require a boolean, not a string. Locate and fix the runtime contract while preserving all other result data. Run relevant Go tests."},
 {"id":"T37","name":"ts-type-error-fix","language":"ts","seed":"ts","edits":["frontend/src/bench/billing.ts|return item.unitPriceCents * item.quantity;|return String(item.unitPriceCents * item.quantity);"],
  "prompt":"Repair the TypeScript type error in frontend/src/bench/billing.ts, preserving numeric cents and checkout behavior. Compile with tsc -p frontend/tsconfig.bench.json from the repo root, and run node --test frontend/src/bench/billing.test.mjs."},
 {"id":"T38","name":"ts-runtime-regression-fix","language":"ts","seed":"ts","edits":["frontend/src/bench/billing.ts|Math.round(subtotal * taxBps / 10000)|Math.floor(subtotal * taxBps / 10000)"],
  "prompt":"A billing regression introduced a subtle cents-rounding defect without any type error. Fix it in the TypeScript domain and preserve interfaces and other behavior; run the bench tests and type checking."},
 {"id":"T39","name":"python-type-runtime-fix","language":"py","seed":"py","edits":["backend/benchdomain/pricing.py|return Invoice(subtotal, discount, subtotal - discount)|return Invoice(str(subtotal), discount, subtotal - discount)"],
  "prompt":"The Python invoicing function has a return-type regression that also breaks runtime behavior. Fix the implementation, not the types or tests. Validate the benchdomain unit tests and Python type checking."},
 {"id":"T40","name":"python-feature-addition","language":"py","seed":"py","edits":[],
  "prompt":"Add owner_summary(orders: list[Order], owner: str, is_admin: bool = False) -> dict[str,int] to backend/benchdomain/reporting.py. Return exactly total, with_description, without_description; whitespace-only description is absent. Owners see their orders, admins see all. Reuse existing visible_orders and has_description. Add focused tests and validate."},
 {"id":"T41","name":"python-behavior-change","language":"py","seed":"py","edits":["backend/benchdomain/orders.py|return order.description is not None and bool(order.description.strip())|return order.description is not None"],
  "prompt":"The behavior contract requires a description to be meaningful only when non-null and non-blank after whitespace trimming. Existing Python code regressed. Correct has_description without changing visibility behavior and run tests."},
 {"id":"T42","name":"ts-interface-implementation","language":"ts","seed":"ts","edits":["frontend/src/bench/types.ts|  list(): LineItem[];|  list(): LineItem[];\n  findAllBySku(sku: string): LineItem[];"],
  "prompt":"InventoryPort gained findAllBySku(sku: string): LineItem[] but InMemoryInventory does not implement it. Implement the new method returning all matching items, preserving duplicates and existing findBySku/list behavior. Compile and test."},
 {"id":"T43","name":"go-semantic-rename","language":"go","seed":"go","edits":[],
  "prompt":"Semantically rename the tools package constructor New (declared in internal/tools/engine.go) to NewEngine, updating every true usage across cmd/ and internal/ including tests. Do not touch unrelated New functions and do not leave an alias. Run go test ./... and go vet ./... ."},
 {"id":"T44","name":"python-complex-root-cause","language":"py","seed":"py","read_only":True,"edits":["backend/benchdomain/orders.py|return [order for order in orders if order.owner == owner]|return [order for order in orders if order.owner != owner]"],
  "prompt":"A customer sees another owner's orders through owner_invoice despite type checking passing. Diagnose the exact defect and dependency chain from owner_invoice through its helpers; state which function contains the error and what change would fix it. Do not edit."},
 {"id":"T45","name":"ts-missing-import-code-action","language":"ts","seed":"ts","edits":["frontend/src/bench/billing.ts|import type { LineItem, InvoiceSummary } from \"./types.ts\";|import type { LineItem } from \"./types.ts\";"],
  "prompt":"A missing TypeScript type import broke compilation of the billing module. Restore type correctness with the smallest source edit and preserve runtime behavior. Compile and run tests."},
 {"id":"T46","name":"large-repo-defect","language":"py","seed":"py","edits":["backend/benchdomain/pricing.py|return (subtotal_cents * rate_basis_points + 5000) // 10000|return (subtotal_cents * rate_basis_points) // 10000"],
  "prompt":"In this full-stack FastAPI repository, a narrow invoicing regression changed round-half-up discounts to floor division. Diagnose the code path and fix only the actual defect in backend/benchdomain. Run tests and type checks; do not edit unrelated API/UI."},
 {"id":"T47","name":"go-compound-workflow","language":"go","seed":"go","edits":[GO_TYPE_FIX,GO_RUNTIME_FIX],
  "prompt":"Diagnose and repair two independent Go regressions: an engine constructor type error and a runtime contract bug where the rename response applied flag is no longer boolean. Trace code/tests, repair both, and run go test ./... and go vet ./... without unrelated edits."},
]
def validate():
 assert len(CASES)==15 and [c["id"] for c in CASES]==[f"T{i}" for i in range(33,48)]
 for c in CASES:
  assert c["seed"] in {"go","ts","py"}
  for op in c.get("edits",[]):assert op.count("|")==2
 return True
if __name__=="__main__":
 validate()
 print(json.dumps([{k:c.get(k) for k in ("id","name","seed","read_only")} for c in CASES],indent=2))
