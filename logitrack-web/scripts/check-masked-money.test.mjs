import assert from "node:assert/strict";
import test from "node:test";
import ts from "typescript";
import { checkSource, monetaryMisuse } from "./check-masked-money.mjs";

for (const source of [
  `import { formatThb } from "./shared";`,
  `import { formatThb as money } from "./shared";`,
  `export { formatThb as money } from "./shared";`,
  `const { formatThb: money } = await import("./shared");`,
]) test(`direct import/re-export: ${source}`, () => assert.equal(checkSource(source, "features/trucks/fixture.ts").length, 1));
test("safe dates and counts have no direct formatter finding", () => assert.deepEqual(checkSource(`import {formatDay} from "./shared"; const count = 0;`, "fixture.ts"), []));

function typed(source) {
  const name = "/fixture.ts";
  const file = ts.createSourceFile(name, source, ts.ScriptTarget.Latest, true);
  const host = ts.createCompilerHost({ strict: true });
  const original = host.getSourceFile;
  host.getSourceFile = (filename, ...args) => filename === name ? file : original.call(host, filename, ...args);
  const program = ts.createProgram([name], { strict: true, noEmit: true }, host);
  return monetaryMisuse(file, program.getTypeChecker());
}
const header = `type MoneyValue = number | null; declare const row: { netPayThb: MoneyValue };`;
for (const expr of ["row.netPayThb ?? 0", "row.netPayThb || 0", "Number(row.netPayThb)", "+row.netPayThb", "formatThb(row.netPayThb)", "row.netPayThb + 1"]) {
  test(`money coercion rejected: ${expr}`, () => assert.ok(typed(`${header} declare function formatThb(v: unknown): string; const value = ${expr};`).length > 0));
}
test("payroll zero substitution survives aliases/destructuring", () => {
  const findings = typed(`${header} const { netPayThb: pay } = row; const alias = pay; const display = alias ?? 0;`);
  assert.ok(findings.some((finding) => finding.message.includes("zero")));
});
test("safe missing counts may use zero", () => assert.equal(typed(`declare const row: { count: number | null }; const count = row.count ?? 0;`).length, 0));

test("current payroll aggregate cannot turn masked net pay into zero", () => {
  const findings = typed(`type MoneyValue = number | null; declare const runs: {netPayThb: MoneyValue}[]; const loadedNet = runs.reduce((acc, r) => acc + (r.netPayThb || 0), 0);`);
  assert.ok(findings.some((finding) => finding.message.includes("zero")));
});

for (const expression of [
 "amounts.map(Number).reduce((sum, n) => sum + n, 0)",
 "amounts.map(value => Number(value)).reduce((sum, n) => sum + n, 0)",
 "amounts.reduce((sum, n) => sum + Number(n), 0)",
 "amounts.filter(value => value !== null).reduce((sum, n) => sum + n, 0)",
]) test(`money collection coercion rejected: ${expression}`, () => {
 const findings = typed(`type MoneyValue = number | null; declare const amounts: MoneyValue[]; const total = ${expression};`);
 assert.ok(findings.length > 0);
});
test("ordinary numeric collections remain allowed", () => assert.equal(typed(`declare const amounts: number[]; const total = amounts.map(Number).reduce((sum,n)=>sum+n,0);`).length, 0));

test("header baseline rejects additions and requires resolved TODO removal", async () => {
 const { headerRatchet } = await import("./check-masked-money.mjs");
 const baseline = { "old.ts": 2 };
 assert.equal(headerRatchet({ "old.ts": 2 }, baseline)[0].severity, "warning");
 for (const current of [{ "old.ts": 3 }, { "old.ts": 2, "new.ts": 1 }, { "old.ts": 1 }, {}]) {
  assert.ok(headerRatchet(current, baseline).some(item => item.severity === "error"));
 }
 assert.equal(headerRatchet({ "old.ts": 2 }, baseline, true)[0].severity, "error");
});
