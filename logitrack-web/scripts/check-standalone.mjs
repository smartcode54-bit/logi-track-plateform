#!/usr/bin/env node
// Post-build gate for the standalone server (developer-spec.md §10.12, §17.3 item 3; TW2).
// Run after `pnpm build` from logitrack-web/: `pnpm check:standalone`. CI runs the build with
// dummy NEXT_PUBLIC_* values and sentinel values for the server-only names below, then this.
//
//  1. .next/standalone/logitrack-web/server.js exists (outputFileTracingRoot is the repo root).
//  2. No .env* file was traced into .next/standalone (server values are runtime env only).
//  3. No server-only env value (when set while building) appears in browser-facing output: the
//     client bundles (.next/static) and the pages prerendered at build time (.next/server/app and
//     .next/server/pages: HTML, RSC payloads and segments, route-handler bodies, .meta headers),
//     which the server returns as they are. Pages rendered on demand cannot be covered by a
//     build-time scan; the image build sets no sentinels, so only CI runs this value scan.
//  4. Source code references only the allow-listed NEXT_PUBLIC_* names (§16.1, web-public).
//  5. The browser holds no Go host (R41, T17): only server code (route handlers under app/api,
//     proxy.ts, and modules that `import "server-only"`) names GO_API_INTERNAL_URL, and no source
//     file names a Go listener of the compose stack (api:8080, localhost:8081, ...); the browser
//     reaches Go only through lib/goFetch.ts and the same-origin BFF /api/go/v1/*.
// Checks 4 and 5 need no build: `checkSources()` runs them (also from `pnpm test`).
// Prints names and file paths only, never values.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const defaultWebDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

// developer-spec.md §16.1, consumer web-server (runtime env of the `web` container; never inlined).
export const SERVER_ONLY = [
  "GO_API_INTERNAL_URL",
  "GO_API_INTERNAL_TIMEOUT_MS",
  "SESSION_COOKIE_DOMAIN",
  "SESSION_COOKIE_SECURE",
  "WEB_PUBLIC_ORIGIN",
  "JWT_ISSUER",
  "JWT_AUDIENCE",
  "JWT_ACCESS_TTL",
  "REFRESH_TOKEN_TTL_WEB",
];

// developer-spec.md §16.1, consumer web-public. Firebase and App Check leave with TW7 (P6).
export const NEXT_PUBLIC_ALLOW = new Set([
  "NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID",
  "NEXT_PUBLIC_FIREBASE_API_KEY",
  "NEXT_PUBLIC_FIREBASE_AUTH_DOMAIN",
  "NEXT_PUBLIC_FIREBASE_PROJECT_ID",
  "NEXT_PUBLIC_FIREBASE_STORAGE_BUCKET",
  "NEXT_PUBLIC_FIREBASE_MESSAGING_SENDER_ID",
  "NEXT_PUBLIC_FIREBASE_APP_ID",
  "NEXT_PUBLIC_FIREBASE_MEASUREMENT_ID",
  "NEXT_PUBLIC_APP_CHECK_RECAPTCHA_SITE_KEY",
  "NEXT_PUBLIC_APP_CHECK_USE_ENTERPRISE",
  "NEXT_PUBLIC_APP_CHECK_DEBUG_TOKEN",
]);

const SOURCE_DIRS = ["app", "components", "context", "features", "firebase", "hooks", "lib", "types", "validate"];
const SOURCE_EXT = /\.(c|m)?(j|t)sx?$/;
// Tests are never bundled; they may name forbidden variables on purpose.
const TEST_FILE = /(\.test\.|\.spec\.|[\\/]__tests__[\\/])/;
// A full name: ends in a letter or digit (a template prefix such as `NEXT_PUBLIC_FIREBASE_${k}` is skipped).
const NEXT_PUBLIC_RX = /NEXT_PUBLIC_[A-Z0-9_]*[A-Z0-9]\b/g;
// A Go listener of the compose stack (developer-spec.md §15: internal :8080, public :8081).
const GO_HOST_RX = /\b(?:api|localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1?\]):808[01]\b/;
const SERVER_ONLY_IMPORT_RX = /^\s*import\s+["']server-only["']\s*;?\s*$/m;

/** Server code may read the Go URL: route handlers, the edge gate and `server-only` modules. */
function isServerSource(rel, text) {
  return rel.startsWith("app/api/") || rel === "proxy.ts" || rel === "middleware.ts" || SERVER_ONLY_IMPORT_RX.test(text);
}

function* walk(dir, skip = () => false) {
  let entries;
  try {
    entries = fs.readdirSync(dir, { withFileTypes: true });
  } catch {
    return;
  }
  for (const e of entries) {
    const p = path.join(dir, e.name);
    if (skip(p, e)) continue;
    if (e.isDirectory()) yield* walk(p, skip);
    else yield p;
  }
}

// Build-time prerender output the server sends as is: page HTML, RSC payloads (`*.rsc`, also under
// `*.segments/`), prerendered route-handler bodies (`*.body`) and their headers (`*.meta`).
const PRERENDER_FILE = /\.(html|rsc|body|meta)$/;

/** Files of the build that reach browsers unchanged: `.next/static` and the prerendered pages. */
function* browserFacingFiles(webDir) {
  yield* walk(path.join(webDir, ".next", "static"));
  for (const dir of ["app", "pages"]) {
    for (const f of walk(path.join(webDir, ".next", "server", dir))) {
      if (PRERENDER_FILE.test(f)) yield f;
    }
  }
}

/**
 * @param {{ env?: Record<string, string | undefined>, webDir?: string }} [options]
 * @returns {{ problems: string[], sentinelsChecked: string[] }}
 */
export function check({ env = process.env, webDir = defaultWebDir } = {}) {
  const rel = (p) => path.relative(path.resolve(webDir, ".."), p);
  const problems = [];
  const standalone = path.join(webDir, ".next", "standalone");
  const serverJs = path.join(standalone, "logitrack-web", "server.js");
  if (!fs.existsSync(serverJs)) {
    problems.push(`missing ${rel(serverJs)} (run pnpm build; output must be "standalone")`);
  }

  for (const f of walk(standalone, (p, e) => e.isDirectory() && e.name === "node_modules")) {
    if (path.basename(f).startsWith(".env")) problems.push(`env file traced into the standalone output: ${rel(f)}`);
  }

  const sentinels = SERVER_ONLY.filter((n) => (env[n] ?? "").length >= 8).map((n) => [n, env[n]]);
  if (sentinels.length > 0) {
    for (const f of browserFacingFiles(webDir)) {
      const text = fs.readFileSync(f, "latin1");
      for (const [name, value] of sentinels) {
        if (text.includes(value)) problems.push(`value of server-only ${name} found in browser-facing output ${rel(f)}`);
      }
    }
  }

  problems.push(...checkSources({ webDir }));
  return { problems: [...new Set(problems)].sort(), sentinelsChecked: sentinels.map(([n]) => n) };
}

/**
 * Checks 4 and 5 over the source tree (no build needed).
 * @param {{ webDir?: string }} [options]
 * @returns {string[]}
 */
export function checkSources({ webDir = defaultWebDir } = {}) {
  const rel = (p) => path.relative(path.resolve(webDir, ".."), p);
  const webRel = (p) => path.relative(webDir, p).split(path.sep).join("/");
  const problems = [];
  const files = SOURCE_DIRS.flatMap((dir) => [...walk(path.join(webDir, dir), (p, e) => e.isDirectory() && e.name === "node_modules")]);
  for (const name of ["proxy.ts", "middleware.ts"]) {
    if (fs.existsSync(path.join(webDir, name))) files.push(path.join(webDir, name));
  }
  for (const f of files) {
    if (!SOURCE_EXT.test(f) || TEST_FILE.test(f)) continue;
    const text = fs.readFileSync(f, "utf8");
    for (const m of text.matchAll(NEXT_PUBLIC_RX)) {
      if (!NEXT_PUBLIC_ALLOW.has(m[0])) problems.push(`${m[0]} in ${rel(f)} is not an allowed NEXT_PUBLIC_ name (developer-spec.md §16.1)`);
    }
    if (GO_HOST_RX.test(text)) {
      problems.push(`${rel(f)} names a Go listener; the browser reaches Go only through /api/go (R41)`);
    }
    if (text.includes("GO_API_INTERNAL_URL") && !isServerSource(webRel(f), text)) {
      problems.push(`${rel(f)} reads GO_API_INTERNAL_URL outside server code (app/api, proxy.ts, "server-only" modules; R41)`);
    }
  }
  return [...new Set(problems)].sort();
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const { problems, sentinelsChecked } = check();
  if (problems.length > 0) {
    for (const p of problems) console.error(`check-standalone: ${p}`);
    console.error(`check-standalone: ${problems.length} problem(s)`);
    process.exit(1);
  }
  const s = sentinelsChecked.length ? sentinelsChecked.join(", ") : "none set (no value scan)";
  console.log(`check-standalone: ok (server.js present, no traced .env, server-only values scanned: ${s})`);
}
