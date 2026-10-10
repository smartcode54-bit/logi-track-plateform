// TW2 (developer-spec.md §10.12, Appendix E §E.9): the standalone server replaces the static
// export, its headers carry the Firebase Hosting rules over, and Hosting becomes a redirect.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, expect, it } from "vitest";
import nextConfig, { APP_CACHE_CONTROL, PAGE_CACHE_CONTROL, PAGE_SOURCE, SECURITY_HEADERS } from "../next.config";
import { check, NEXT_PUBLIC_ALLOW, SERVER_ONLY } from "../scripts/check-standalone.mjs";

const webDir = path.resolve(__dirname, "..");

/** Next's matcher for a `source` without named-parameter modifiers beyond `(regex)`, `*`. */
function sourceToRegExp(source: string): RegExp {
  if (source === "/:path*") return /^\/.*$/;
  const custom = /^\/:path\((.*)\)$/.exec(source);
  if (custom) return new RegExp(`^/${custom[1]}$`);
  const star = /^(.*)\/:path\*$/.exec(source);
  if (star) return new RegExp(`^${star[1]}(?:/.*)?$`);
  return new RegExp(`^${source}$`);
}

async function cacheControlFor(pathname: string): Promise<string | undefined> {
  const rules = (await nextConfig.headers!()) ?? [];
  let value: string | undefined;
  for (const rule of rules) {
    if (!sourceToRegExp(rule.source).test(pathname)) continue;
    for (const h of rule.headers) if (h.key === "Cache-Control") value = h.value; // later rules win
  }
  return value;
}

describe("next.config (standalone)", () => {
  it("always builds a standalone server traced from the monorepo root, without Next compression", () => {
    expect(nextConfig.output).toBe("standalone");
    expect(nextConfig.outputFileTracingRoot).toBe(path.resolve(webDir, ".."));
    expect(nextConfig.turbopack?.root).toBe(nextConfig.outputFileTracingRoot);
    expect(nextConfig.compress).toBe(false);
  });

  it("sends the Hosting COEP rule plus the hardening headers on every path", async () => {
    const rules = (await nextConfig.headers!()) ?? [];
    const all = rules.find((r) => r.source === "/:path*");
    expect(all?.headers).toEqual(SECURITY_HEADERS);
    expect(SECURITY_HEADERS).toContainEqual({ key: "Cross-Origin-Embedder-Policy", value: "unsafe-none" });
  });

  it.each([
    ["/", PAGE_CACHE_CONTROL],
    ["/login", PAGE_CACHE_CONTROL],
    ["/apple", PAGE_CACHE_CONTROL],
    ["/app", APP_CACHE_CONTROL],
    ["/app/dashboard", APP_CACHE_CONTROL],
    ["/app/customers/AbC123", APP_CACHE_CONTROL],
    ["/_next/static/chunks/main.js", undefined],
    ["/api/healthz", undefined],
  ])("Cache-Control for %s", async (pathname, want) => {
    expect(await cacheControlFor(pathname)).toBe(want);
  });

  it("matches the Hosting document rule and keeps /app private", () => {
    expect(PAGE_CACHE_CONTROL).toBe("public, max-age=0, must-revalidate");
    expect(APP_CACHE_CONTROL).toBe("private, no-cache");
    expect(PAGE_SOURCE.startsWith("/:path(")).toBe(true);
  });

  it("turns the legacy redirect pages into permanent redirects", async () => {
    expect(await nextConfig.redirects!()).toEqual([
      { source: "/app/users", destination: "/app/security-center/users", permanent: true },
      { source: "/app/operations/roles", destination: "/app/security-center/roles", permanent: true },
    ]);
  });
});

describe("dynamic [id] routes", () => {
  it.each([
    "app/app/customers/[id]/page.tsx",
    "app/app/customers/[id]/edit/page.tsx",
    "app/app/subcontractors/[id]/page.tsx",
    "app/app/subcontractors/[id]/edit/page.tsx",
  ])("%s has no generateStaticParams placeholder", (file) => {
    const src = fs.readFileSync(path.join(webDir, file), "utf8");
    expect(src).not.toMatch(/generateStaticParams|placeholder/);
  });
});

describe("Firebase Hosting (redirect until P8)", () => {
  it.each(["firebase.json", "firebase.prod.json"])("%s serves only a 301 to WEB_DOMAIN", (file) => {
    const cfg = JSON.parse(fs.readFileSync(path.join(webDir, file), "utf8"));
    const hosting = cfg.hosting;
    expect(Object.keys(hosting).sort()).toEqual(["ignore", "public", "redirects"]);
    expect(fs.existsSync(path.join(webDir, hosting.public, "index.html"))).toBe(true);
    expect(hosting.redirects).toHaveLength(1);
    const [r] = hosting.redirects;
    expect(r.source).toBe("/:path*");
    expect(r.type).toBe(301);
    // The owner writes the decided host over WEB_DOMAIN in the targeted-deploy commit (§19 Q8, Q18).
    expect(r.destination).toMatch(/^https:\/\/[A-Za-z0-9._-]+\/:path$/);
    // Functions, Firestore and Storage still deploy from these files until P8.
    expect(cfg.functions).toBeDefined();
    expect(cfg.firestore).toBeDefined();
    expect(cfg.storage).toBeDefined();
  });
});

describe("scripts/check-standalone.mjs", () => {
  function fixture(opts: { serverJs?: boolean; envFile?: boolean; leak?: string; source?: string }) {
    const root = fs.mkdtempSync(path.join(os.tmpdir(), "check-standalone-"));
    const web = path.join(root, "logitrack-web");
    const write = (rel: string, body: string) => {
      fs.mkdirSync(path.dirname(path.join(web, rel)), { recursive: true });
      fs.writeFileSync(path.join(web, rel), body);
    };
    if (opts.serverJs !== false) write(".next/standalone/logitrack-web/server.js", "");
    if (opts.envFile) write(".next/standalone/logitrack-web/.env.production", "X=1\n");
    write(".next/static/chunks/app.js", `console.log(${JSON.stringify(opts.leak ?? "")})`);
    write("lib/env.ts", opts.source ?? "export const k = process.env.NEXT_PUBLIC_GOOGLE_OIDC_CLIENT_ID;\n");
    // Test files are skipped by the source scan.
    write("lib/env.test.ts", "process.env.NEXT_PUBLIC_NOT_ALLOWED_IN_APP_CODE;\n");
    return web;
  }
  const env = { GO_API_INTERNAL_URL: "http://sentinel-go.invalid:8080" };

  it("passes a clean standalone tree", () => {
    expect(check({ env, webDir: fixture({}) }).problems).toEqual([]);
  });

  it("fails without server.js, with a traced .env file, a leaked server value or an unknown NEXT_PUBLIC_ name", () => {
    const problems: string[] = check({
      env,
      webDir: fixture({
        serverJs: false,
        envFile: true,
        leak: env.GO_API_INTERNAL_URL,
        source: "export const a = process.env.NEXT_PUBLIC_API_URL; const p = `NEXT_PUBLIC_FIREBASE_${'x'}`;\n",
      }),
    }).problems;
    expect(problems).toHaveLength(4);
    expect(problems.join("\n")).toMatch(/missing .*server\.js/);
    expect(problems.join("\n")).toMatch(/env file traced/);
    expect(problems.join("\n")).toMatch(/server-only GO_API_INTERNAL_URL/);
    expect(problems.join("\n")).toMatch(/NEXT_PUBLIC_API_URL/);
  });

  it("allow-lists exactly the §16.1 web-public names and covers every web-server value", () => {
    expect(NEXT_PUBLIC_ALLOW.size).toBe(11);
    expect(SERVER_ONLY).toContain("GO_API_INTERNAL_URL");
    expect(SERVER_ONLY).not.toContain("NODE_ENV");
  });
});
