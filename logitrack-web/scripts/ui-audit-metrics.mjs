/** Shared UI4 counters and per-file ratchet. Usage: node scripts/ui-audit-metrics.mjs --write-baseline */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const count = (source, regex) => [...source.matchAll(regex)].length;
const parse = (source, filename = "source.tsx") => ts.createSourceFile(filename, source, ts.ScriptTarget.Latest, true);
const printer = ts.createPrinter({ removeComments: true });

/** The TS printer preserves strings, URLs, regex literals and template expressions while removing comments. */
export function stripComments(source, filename = "source.tsx") {
  if (filename.endsWith(".css")) return source.replace(/\/\*[\s\S]*?\*\//g, "");
  return printer.printFile(parse(source, filename));
}

/** Class strings in className/cn()/cva() expressions, counted once even when nested. */
function classStrings(source, filename) {
  const file = parse(source, filename);
  const strings = new Set();
  const contexts = new Map();
  const moduleConstants = new Set();
  const moduleClassConstants = new Map();
  const hasUppercaseToken = (value) => /(?:^|\s)(?:[^\s]+:)?!?uppercase(?=\s|$)/.test(value);

  for (const statement of file.statements) {
    if (!ts.isVariableStatement(statement) || !(statement.declarationList.flags & ts.NodeFlags.Const)) continue;
    for (const declaration of statement.declarationList.declarations) {
      const value = declaration.initializer;
      if (!ts.isIdentifier(declaration.name) || !value || !ts.isStringLiteralLike(value)) continue;
      if (value.text.trim().split(/\s+/).length >= 2 && hasUppercaseToken(value.text)) {
        moduleClassConstants.set(declaration.name.text, value);
        moduleConstants.add(value);
      }
    }
  }

  function add(stringNode, contextNode) {
    strings.add(stringNode);
    const uses = contexts.get(stringNode) ?? new Set();
    uses.add(contextNode);
    contexts.set(stringNode, uses);
  }
  function collect(node, contextNode) {
    if (ts.isStringLiteralLike(node) || ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node)) {
      add(node, contextNode);
      return;
    }
    if (ts.isIdentifier(node)) {
      const isPropertyName = ts.isPropertyAccessExpression(node.parent) && node.parent.name === node;
      const constant = isPropertyName ? undefined : moduleClassConstants.get(node.text);
      if (constant) {
        add(constant, node);
        return;
      }
    }
    ts.forEachChild(node, (child) => collect(child, contextNode));
  }
  function visit(node) {
    if (ts.isJsxAttribute(node) && node.name.getText(file) === "className" && node.initializer) collect(node.initializer, node.initializer);
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && ["cn", "cva"].includes(node.expression.text)) {
      node.arguments.forEach((argument) => collect(argument, argument));
    }
    ts.forEachChild(node, visit);
  }
  visit(file);
  return { file, strings: [...strings], contexts, moduleConstants: [...moduleConstants] };
}

const LOADING_NAME = /^(?:is)?(?:loading|pending|fetching|calculating)/i;
const isLoadingName = (name) => LOADING_NAME.test(name) || /^mounted$/i.test(name);

function unwrap(expression) {
  while (ts.isParenthesizedExpression(expression) || ts.isAsExpression(expression) || ts.isTypeAssertionExpression(expression) || ts.isNonNullExpression(expression)) {
    expression = expression.expression;
  }
  return expression;
}

function isLoadingSignal(expression) {
  const node = unwrap(expression);
  if (ts.isIdentifier(node)) return isLoadingName(node.text);
  if (ts.isPropertyAccessExpression(node)) return isLoadingName(node.name.text);
  if (ts.isPrefixUnaryExpression(node) && node.operator === ts.SyntaxKind.ExclamationToken) return isLoadingSignal(node.operand);
  if (ts.isBinaryExpression(node)) {
    if (isLoadingStatusComparison(node)) return true;
    return isLoadingSignal(node.left) || isLoadingSignal(node.right);
  }
  if (ts.isConditionalExpression(node)) return isLoadingSignal(node.condition);
  return false;
}

function isLoadingStatusComparison(node) {
  if (![ts.SyntaxKind.EqualsEqualsToken, ts.SyntaxKind.EqualsEqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsToken, ts.SyntaxKind.ExclamationEqualsEqualsToken].includes(node.operatorToken.kind)) return false;
  const left = unwrap(node.left), right = unwrap(node.right);
  const status = (expression) => ts.isIdentifier(expression) || ts.isPropertyAccessExpression(expression);
  const loadingText = (expression) => ts.isStringLiteralLike(expression) && /^(loading|pending|fetching|calculating)$/i.test(expression.text);
  return (status(left) && loadingText(right)) || (status(right) && loadingText(left));
}

/** Abstract boolean outcomes for a condition while loading is true; unknown values may be either boolean. */
function truthValues(expression, loading = true) {
  const node = unwrap(expression);
  if (node.kind === ts.SyntaxKind.TrueKeyword) return new Set([true]);
  if (node.kind === ts.SyntaxKind.FalseKeyword) return new Set([false]);
  if (ts.isIdentifier(node) && isLoadingName(node.text)) return new Set([/^mounted$/i.test(node.text) ? !loading : loading]);
  if (ts.isPropertyAccessExpression(node) && isLoadingName(node.name.text)) return new Set([/^mounted$/i.test(node.name.text) ? !loading : loading]);
  if (ts.isPrefixUnaryExpression(node) && node.operator === ts.SyntaxKind.ExclamationToken) {
    return new Set([...truthValues(node.operand, loading)].map((value) => !value));
  }
  if (ts.isBinaryExpression(node)) {
    if (isLoadingStatusComparison(node)) {
      const positive = [ts.SyntaxKind.EqualsEqualsToken, ts.SyntaxKind.EqualsEqualsEqualsToken].includes(node.operatorToken.kind);
      return new Set([positive ? loading : !loading]);
    }
    const left = truthValues(node.left, loading), right = truthValues(node.right, loading);
    if (node.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken) {
      return new Set([...left].flatMap((a) => [...right].map((b) => a && b)));
    }
    if (node.operatorToken.kind === ts.SyntaxKind.BarBarToken) {
      return new Set([...left].flatMap((a) => [...right].map((b) => a || b)));
    }
    if ([ts.SyntaxKind.EqualsEqualsToken, ts.SyntaxKind.EqualsEqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsToken, ts.SyntaxKind.ExclamationEqualsEqualsToken].includes(node.operatorToken.kind)) {
      const positive = [ts.SyntaxKind.EqualsEqualsToken, ts.SyntaxKind.EqualsEqualsEqualsToken].includes(node.operatorToken.kind);
      const leftBoolean = node.left.kind === ts.SyntaxKind.TrueKeyword || node.left.kind === ts.SyntaxKind.FalseKeyword
        ? node.left.kind === ts.SyntaxKind.TrueKeyword
        : undefined;
      const rightBoolean = node.right.kind === ts.SyntaxKind.TrueKeyword || node.right.kind === ts.SyntaxKind.FalseKeyword
        ? node.right.kind === ts.SyntaxKind.TrueKeyword
        : undefined;
      if (leftBoolean !== undefined) return new Set([...truthValues(node.right, loading)].map((value) => positive ? value === leftBoolean : value !== leftBoolean));
      if (rightBoolean !== undefined) return new Set([...truthValues(node.left, loading)].map((value) => positive ? value === rightBoolean : value !== rightBoolean));
    }
  }
  if (ts.isConditionalExpression(node)) {
    const condition = truthValues(node.condition, loading);
    return new Set([...condition].flatMap((value) => [...truthValues(value ? node.whenTrue : node.whenFalse, loading)]));
  }
  return new Set([true, false]);
}

function isWithin(node, ancestor) {
  for (let current = node; current; current = current.parent) if (current === ancestor) return true;
  return false;
}

function rendersDuringLoading(condition, expected) {
  return isLoadingSignal(condition) && truthValues(condition, true).has(expected);
}

/** Pulse is functional only when structure places it in a loading branch or on a busy/status element. */
function loadingPulse(node, file) {
  for (let parent = node.parent; parent; parent = parent.parent) {
    if (ts.isFunctionDeclaration(parent) && /skeleton|loading/i.test(parent.name?.text ?? "")) return true;
    if (ts.isPropertyAssignment(parent) && parent.name.getText(file) === "loading" && (ts.isArrowFunction(parent.initializer) || ts.isFunctionExpression(parent.initializer))) return true;
    if (ts.isIfStatement(parent)) {
      if (isWithin(node, parent.thenStatement) && rendersDuringLoading(parent.expression, true)) return true;
      if (parent.elseStatement && isWithin(node, parent.elseStatement) && rendersDuringLoading(parent.expression, false)) return true;
    }
    if (ts.isConditionalExpression(parent)) {
      if (isWithin(node, parent.whenTrue) && rendersDuringLoading(parent.condition, true)) return true;
      if (isWithin(node, parent.whenFalse) && rendersDuringLoading(parent.condition, false)) return true;
    }
    if (ts.isBinaryExpression(parent) && [ts.SyntaxKind.AmpersandAmpersandToken, ts.SyntaxKind.BarBarToken].includes(parent.operatorToken.kind) && isWithin(node, parent.right)) {
      const rightExecutesDuringLoading = parent.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken
        ? rendersDuringLoading(parent.left, true)
        : rendersDuringLoading(parent.left, false);
      if (rightExecutesDuringLoading) return true;
    }
    if (ts.isJsxElement(parent) || ts.isJsxSelfClosingElement(parent)) {
      const opening = ts.isJsxElement(parent) ? parent.openingElement : parent;
      if (/\b(?:aria-busy=["']true["']|role=["']status["'])/.test(opening.getText(file))) return true;
    }
  }
  return false;
}

function hexOrRgbCount(source, filename) {
  const colorPattern = /(?<![\w&])#(?:[\da-f]{8}|[\da-f]{6}|[\da-f]{3,4})(?![\da-f\w])|\brgba?\([^)]*\)/gi;
  const file = filename.endsWith(".css") ? undefined : parse(source, filename);
  const jsxTextRanges = [];
  if (file) {
    function visit(node) {
      if (ts.isJsxText(node)) jsxTextRanges.push([node.pos, node.end]);
      ts.forEachChild(node, visit);
    }
    visit(file);
  }
  return [...source.matchAll(colorPattern)].filter((match) => !jsxTextRanges.some(([start, end]) => match.index >= start && match.index < end)).length;
}

// Counters take comment-free source. countMetrics is the public entry point for raw source.
export const UI4_METRICS = {
  paletteColors: (s) => count(s, /(?<![\w-])(?:bg|text|border|ring|from|to|fill|stroke|divide)-(?:red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|slate|gray|zinc|neutral|stone)-\d{2,3}\b/g),
  hexOrRgb: hexOrRgbCount,
  tinyText: (s) => count(s, /(?<![\w-])text-\[(?:9|10|11)px\]/g),
  uppercaseClass: (s, filename) => {
    const { strings, moduleConstants } = classStrings(s, filename);
    return [...new Set([...strings, ...moduleConstants])].reduce((n, node) => n + count(node.text, /(?:^|\s)(?:[^\s]+:)?!?uppercase(?=\s|$)/g), 0);
  },
  sideStripe: (s) => count(s, /(?<![\w-])border-(?:l|t)-4(?![\w-])/g),
  blurGlow: (s, filename) => {
    const { file, strings, contexts } = classStrings(s, filename);
    const loading = strings.filter((node) => {
      const uses = [...(contexts.get(node) ?? [])];
      return uses.length > 0 && uses.every((use) => loadingPulse(use, file));
    }).reduce((n, node) => n + count(node.text, /(?<![\w-])animate-pulse(?![\w-])/g), 0);
    return count(s, /\bbackdrop-blur(?:-[\w[\].-]+)?|(?<![\w-])blur-(?:xl|[2-9]xl|[1-9]\d+xl)(?![\w-])|(?<![\w-])shadow-(?:[a-z]+-\d{2,3}|[a-z]+(?:-[a-z]+)*)\/\d+|(?<![\w-])animate-(?:ping|pulse)(?![\w-])/g) - loading;
  },
  invalidHslVar: (s) => count(s, /\bhsl\(\s*var\(\s*--[\w-]+\s*\)\s*\)/g),
};
export const METRIC_NAMES = Object.keys(UI4_METRICS).sort();

export function countMetrics(source, filename = "source.tsx") {
  const clean = stripComments(source, filename);
  return Object.fromEntries(METRIC_NAMES.map((name) => [name, UI4_METRICS[name](clean, filename)]));
}

/** Only colours over photos are exempt; typography, stripes and effects still ratchet. */
export const COLOR_OVERLAY_ALLOW_LIST = ["components/ui/file-viewer.tsx", "components/accounting/ImagePreviewGallery.tsx"];

/** Pure rule: counts includes every existing source path, even files with all-zero metrics. */
export function checkRatchet(counts, baseline, manifests = []) {
  const errors = [];
  const cleanPaths = new Set(manifests.flat());
  for (const filename of [...cleanPaths].sort()) {
    if (!Object.hasOwn(counts, filename)) errors.push(`${filename}: manifest path does not exist in the source tree`);
  }
  for (const filename of Object.keys(counts).sort()) {
    for (const metric of METRIC_NAMES) {
      const actual = counts[filename][metric] ?? 0;
      let allowed = baseline[filename]?.[metric] ?? 0;
      if (!cleanPaths.has(filename) && COLOR_OVERLAY_ALLOW_LIST.includes(filename) && ["paletteColors", "hexOrRgb"].includes(metric)) continue;
      if (filename === "components/app-sidebar.tsx" && metric === "tinyText") allowed = Math.min(allowed, 2);
      if (cleanPaths.has(filename)) allowed = 0;
      if (actual > allowed) errors.push(`${filename}: ${metric} ${actual} > ${allowed}${cleanPaths.has(filename) ? " (clean manifest)" : ""}`);
    }
  }
  return errors;
}

export function sourceFiles(webDir) {
  function walk(dir) {
    return fs.readdirSync(dir, { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name, "en")).flatMap((entry) => {
      const filename = path.join(dir, entry.name);
      if (entry.isDirectory()) return ["node_modules", ".next", "__tests__", "tests"].includes(entry.name) ? [] : walk(filename);
      return /\.(tsx?|css)$/.test(entry.name) && !/\.(?:test|spec)\.(tsx?|css)$/.test(entry.name) ? [filename] : [];
    });
  }
  return ["app", "components", "features"].flatMap((dir) => walk(path.join(webDir, dir))).map((filename) => ({
    rel: path.relative(webDir, filename).split(path.sep).join("/"),
    src: fs.readFileSync(filename, "utf8"),
  })).sort((a, b) => a.rel < b.rel ? -1 : a.rel > b.rel ? 1 : 0);
}

export function scanMetrics(webDir) {
  return Object.fromEntries(sourceFiles(webDir).map(({ rel, src }) => [rel, countMetrics(src, rel)]));
}

export function readCleanManifests(webDir) {
  const dir = path.join(webDir, "lib/ui4-clean");
  if (!fs.existsSync(dir)) return [];
  return fs.readdirSync(dir).filter((name) => name.endsWith(".json")).sort().map((name) => {
    const paths = JSON.parse(fs.readFileSync(path.join(dir, name), "utf8"));
    if (!Array.isArray(paths) || paths.some((p) => typeof p !== "string" || !/^(app|components|features)\//.test(p) || p.split("/").some((part) => ["..", ".", ""].includes(part)) || p.includes("\\"))) {
      throw new Error(`${name}: expected an array of logitrack-web-relative production source paths`);
    }
    return paths;
  });
}

/** Sparse baseline: sorted paths and metric keys; zero files need no allowance. */
export function serializeBaseline(counts) {
  const baseline = Object.fromEntries(Object.keys(counts).sort().flatMap((filename) => {
    const metrics = Object.fromEntries(METRIC_NAMES.filter((metric) => counts[filename][metric] > 0).map((metric) => [metric, counts[filename][metric]]));
    return Object.keys(metrics).length ? [[filename, metrics]] : [];
  }));
  return `${JSON.stringify(baseline, null, 2)}\n`;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  if (process.argv.length !== 3 || process.argv[2] !== "--write-baseline") throw new Error("Usage: node scripts/ui-audit-metrics.mjs --write-baseline");
  const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const counts = scanMetrics(webDir);
  fs.writeFileSync(path.join(webDir, "lib/ui4Palette.baseline.json"), serializeBaseline(counts));
  console.log(`Wrote UI4 baseline for ${Object.keys(counts).length} source files.`);
}
