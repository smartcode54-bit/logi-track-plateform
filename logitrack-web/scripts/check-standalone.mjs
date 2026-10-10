#!/usr/bin/env node
// Post-build gate for the standalone server (developer-spec.md §10.12, §17.3 item 3; TW2).
// Run after `pnpm build` from logitrack-web/: `pnpm check:standalone`. CI runs the build with
// dummy NEXT_PUBLIC_* values and sentinel values for the server-only names below, then this.
//
//  1. .next/standalone/logitrack-web/server.js exists (outputFileTracingRoot is the repo root).
//  2. No .env* file was traced into .next/standalone (server values are runtime env only).
//  3. No server-only env value (when set while building) appears in the browser bundles (.next/static).
//  4. Source code references only the allow-listed NEXT_PUBLIC_* names (§16.1, web-public).
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

  const staticDir = path.join(webDir, ".next", "static");
  const sentinels = SERVER_ONLY.filter((n) => (env[n] ?? "").length >= 8).map((n) => [n, env[n]]);
  if (sentinels.length > 0) {
    for (const f of walk(staticDir)) {
      const text = fs.readFileSync(f, "latin1");
      for (const [name, value] of sentinels) {
        if (text.includes(value)) problems.push(`value of server-only ${name} found in browser bundle ${rel(f)}`);
      }
    }
  }

  for (const dir of SOURCE_DIRS) {
    for (const f of walk(path.join(webDir, dir), (p, e) => e.isDirectory() && e.name === "node_modules")) {
      if (!SOURCE_EXT.test(f) || TEST_FILE.test(f)) continue;
      for (const m of fs.readFileSync(f, "utf8").matchAll(NEXT_PUBLIC_RX)) {
        if (!NEXT_PUBLIC_ALLOW.has(m[0])) problems.push(`${m[0]} in ${rel(f)} is not an allowed NEXT_PUBLIC_ name (developer-spec.md §16.1)`);
      }
    }
  }
  return { problems: [...new Set(problems)].sort(), sentinelsChecked: sentinels.map(([n]) => n) };
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
