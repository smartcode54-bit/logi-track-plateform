import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const phaseTrees = /^(features\/(dashboard|trucks|drivers|maintenance|subcontractors)\/|app\/app\/(dashboard|trucks|truck-assignment|drivers|renewals|maintenance|subcontractors)\/|app\/app\/page\.tsx$)/;
const testFile = /(^|\/)(__tests__|tests|test-utils|fixtures)(\/|$)|\.(test|spec)\.[cm]?[jt]sx?$/;
// These two existing imports belong to P1.6. New files and added import sites fail.
export const formatterTodos = new Map([
  ["features/maintenance/components/go/MaintenanceOverviewPage.tsx", 1],
  ["features/drivers/components/go/TripDetailDialog.tsx", 1],
]);
const forbiddenHeader = new RegExp(["x", "act", "on", "tenant"].join("-") + "|" + ["ACT", "ON", "TENANT", "HEADER"].join("_"), "i");
const headerBaseline = JSON.parse(fs.readFileSync(new URL("./support-header-baseline.json", import.meta.url), "utf8"));
const normalize = (file) => file.replaceAll(path.sep, "/");

export function directFormatterImports(source, checker) {
  const findings = [];
  const report = (node) => findings.push({ start: node.getStart(source), rule: "direct-formatter", message: "Use the shared checked money formatter (P1.6 TODO for existing imports)." });
  const visit = (node) => {
    if (ts.isImportSpecifier(node) || ts.isExportSpecifier(node)) {
      const symbol = checker?.getSymbolAtLocation(node.name);
      const resolved = symbol && (symbol.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(symbol) : symbol);
      if ((node.propertyName ?? node.name).text === "formatThb" || resolved?.name === "formatThb") report(node);
    }
    if (ts.isPropertyAccessExpression(node) && node.name.text === "formatThb") {
      const symbol = checker?.getSymbolAtLocation(node.expression);
      if (symbol?.declarations?.some((decl) => ts.isNamespaceImport(decl))) report(node);
    }
    if (ts.isExportDeclaration(node) && (!node.exportClause || ts.isNamespaceExport(node.exportClause)) && node.moduleSpecifier && checker) {
      const sourceModule = checker.getSymbolAtLocation(node.moduleSpecifier);
      if (sourceModule && checker.getExportsOfModule(sourceModule).some((item) => item.name === "formatThb")) report(node);
    }
    if (ts.isBindingElement(node) && (node.propertyName ?? node.name).getText(source) === "formatThb") report(node);
    ts.forEachChild(node, visit);
  };
  visit(source);
  return findings;
}

// The type checker carries MoneyValue through properties, aliases, destructuring
// and generic return types; arithmetic results retain taint through local aliases.
export function monetaryMisuse(source, checker) {
  const findings = [];
  const tainted = new Set();
  const moneyType = (type) => !!type && (["MoneyValue", "DecimalMoneyValue"].includes(type.aliasSymbol?.name) || (type.isUnion?.() && type.types.some((part) => ["MoneyValue", "DecimalMoneyValue"].includes(part.aliasSymbol?.name))));
  const monetaryType = (type, seen = new Set()) => {
    if (!type || seen.has(type)) return false;
    seen.add(type);
    if (moneyType(type)) return true;
    const element = checker.getIndexTypeOfType(type, ts.IndexKind.Number);
    return !!element && monetaryType(element, seen);
  };
  const money = (node) => monetaryType(checker.getTypeAtLocation(node)) || tainted.has(checker.getSymbolAtLocation(node));
  const containsMoney = (node) => { let found = money(node); ts.forEachChild(node, (child) => { if (containsMoney(child)) found = true; }); return found; };
  let changed;
  do {
    const before = tainted.size;
    const collect = (node) => {
      if (ts.isVariableDeclaration(node) && node.initializer && containsMoney(node.initializer) && ts.isIdentifier(node.name)) tainted.add(checker.getSymbolAtLocation(node.name));
      if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && containsMoney(node.expression.expression)) {
        for (const callback of node.arguments) {
          if (ts.isArrowFunction(callback) || ts.isFunctionExpression(callback)) {
            for (const parameter of callback.parameters) if (ts.isIdentifier(parameter.name)) tainted.add(checker.getSymbolAtLocation(parameter.name));
          }
        }
      }
      ts.forEachChild(node, collect);
    };
    collect(source);
    changed = tainted.size !== before;
  } while (changed);
  const report = (node, message) => findings.push({ start: node.getStart(source), rule: "masked-money", message });
  const visit = (node) => {
    if (ts.isBinaryExpression(node)) {
      const op = node.operatorToken.kind;
      if ([ts.SyntaxKind.QuestionQuestionToken, ts.SyntaxKind.BarBarToken].includes(op) && containsMoney(node.left) && ts.isNumericLiteral(node.right) && Number(node.right.text) === 0) report(node, "Redacted money cannot default to zero.");
      else if ([ts.SyntaxKind.PlusToken, ts.SyntaxKind.MinusToken, ts.SyntaxKind.AsteriskToken, ts.SyntaxKind.SlashToken, ts.SyntaxKind.PlusEqualsToken].includes(op) && (containsMoney(node.left) || containsMoney(node.right))) report(node, "Aggregate money through the checked masked-money helper.");
    }
    if (ts.isPrefixUnaryExpression(node) && [ts.SyntaxKind.PlusToken, ts.SyntaxKind.MinusToken].includes(node.operator) && containsMoney(node.operand)) report(node, "Do not coerce nullable money.");
    if (ts.isCallExpression(node) && (node.arguments.some(containsMoney) || (ts.isPropertyAccessExpression(node.expression) && containsMoney(node.expression.expression)))) {
      const symbol = checker.getSymbolAtLocation(node.expression);
      const resolved = symbol && (symbol.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(symbol) : symbol);
      const checkedHelper = resolved?.declarations?.some((decl) => normalize(decl.getSourceFile().fileName).endsWith("/lib/maskedValue.ts"));
      if (!checkedHelper) report(node, "Format or coerce money only through the shared checked helper.");
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
  return findings;
}

export function checkSource(text, filename) {
  const source = ts.createSourceFile(filename, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  return directFormatterImports(source);
}

function productionFiles(root) {
  const result = [];
  const walk = (dir) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      if (["node_modules", ".next", ".git", "out", "build", ".firebase", "coverage", "playwright-report", "test-results"].includes(entry.name)) continue;
      const file = path.join(dir, entry.name);
      const relative = normalize(path.relative(root, file));
      if (testFile.test(relative) || relative.startsWith("functions/lib/")) continue;
      if (entry.isDirectory()) walk(file);
      else if (/\.[cm]?[jt]sx?$/.test(entry.name)) result.push(file);
    }
  };
  walk(root);
  return result;
}

export function headerRatchet(current, baseline, strict = false) {
  const diagnostics = [];
  for (const [file, count] of Object.entries(current)) diagnostics.push({ file, rule: "human-header", severity: strict || count > (baseline[file] ?? 0) ? "error" : "warning", message: `D1 migration TODO: ${count} retired header sites (baseline ${baseline[file] ?? 0}).` });
  if (!strict) for (const [file, count] of Object.entries(baseline)) if ((current[file] ?? 0) < count) diagnostics.push({ file, rule: "human-header", severity: "error", message: "Remove the resolved/stale header baseline entry." });
  return diagnostics;
}

export function run(root, mode = "all", strict = false) {
  const diagnostics = [];
  const files = productionFiles(root);
  if (mode !== "money") {
    const current = {};
    for (const file of files) {
      const count = [...fs.readFileSync(file, "utf8").matchAll(new RegExp(forbiddenHeader, "gi"))].length;
      if (count) current[normalize(path.relative(root, file))] = count;
    }
    diagnostics.push(...headerRatchet(current, headerBaseline, strict));
  }
  if (mode !== "headers") {
    const config = ts.readConfigFile(path.join(root, "tsconfig.json"), ts.sys.readFile);
    const parsed = ts.parseJsonConfigFileContent(config.config, ts.sys, root);
    const program = ts.createProgram(parsed.fileNames, { ...parsed.options, noEmit: true });
    const checker = program.getTypeChecker();
    for (const file of files) {
      const relative = normalize(path.relative(root, file));
      if (!phaseTrees.test(relative)) continue;
      const source = program.getSourceFile(file);
      if (!source) continue;
      const imports = directFormatterImports(source, checker);
      for (const [index, finding] of imports.entries()) diagnostics.push({ file: relative, ...finding, severity: index < (formatterTodos.get(relative) ?? 0) ? "warning" : "error" });
      for (const finding of monetaryMisuse(source, checker)) diagnostics.push({ file: relative, ...finding, severity: "error" });
    }
  }
  return diagnostics;
}

export const supportAccessPlugin = {
  rules: {
    "checked-money-import": {
      meta: { type: "problem", schema: [], messages: { checked: "Use MaskedValue or the shared checked money formatter; this direct formatter import is a P1.6 migration TODO." } },
      create(context) {
        const namespaces = new Set();
        return {
          ImportSpecifier(node) { if ((node.imported.name ?? node.imported.value) === "formatThb") context.report({ node, messageId: "checked" }); },
          ExportSpecifier(node) { if ((node.local.name ?? node.local.value) === "formatThb") context.report({ node, messageId: "checked" }); },
          ImportNamespaceSpecifier(node) { namespaces.add(node.local.name); },
          MemberExpression(node) { if (node.object.type === "Identifier" && namespaces.has(node.object.name) && node.property.name === "formatThb") context.report({ node, messageId: "checked" }); },
          ExportAllDeclaration(node) { if (node.source.value.includes("accounting/components/go/shared")) context.report({ node, messageId: "checked" }); },
        };
      },
    },
  },
};

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const mode = process.argv.includes("--headers") ? "headers" : process.argv.includes("--money") ? "money" : "all";
  const diagnostics = run(root, mode, process.argv.includes("--strict"));
  for (const item of diagnostics) console.error(`${item.severity}: ${item.file}: ${item.rule}: ${item.message}`);
  console.log(`support contracts: ${diagnostics.filter((item) => item.severity === "error").length} errors, ${diagnostics.filter((item) => item.severity === "warning").length} migration TODOs`);
  process.exitCode = diagnostics.some((item) => item.severity === "error") ? 1 : 0;
}
