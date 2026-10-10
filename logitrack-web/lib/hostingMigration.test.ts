// TW2 (developer-spec.md §10.12, Appendix E §E.9): the standalone server replaces the static
// export, its headers carry the Firebase Hosting rules over, and Hosting becomes a redirect.
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { describe, expect, it } from "vitest";
import nextConfig, {
  APP_CACHE_CONTROL,
  ASSET_CACHE_CONTROL,
  PAGE_CACHE_CONTROL,
  PAGE_SOURCE,
  SECURITY_HEADERS,
} from "../next.config";
import { checkHostingRedirect } from "../scripts/check-hosting-redirect.mjs";
import { check, checkSources, NEXT_PUBLIC_ALLOW, SERVER_ONLY } from "../scripts/check-standalone.mjs";
import { prepareStandalone } from "../scripts/start-standalone.mjs";

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

function* walkFiles(dir: string): Generator<string> {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) yield* walkFiles(p);
    else yield p;
  }
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
    ["/_next/static/media/font.woff2", undefined],
    ["/api/healthz", undefined],
    // Unhashed public/ and metadata files keep Hosting's default one-hour cache.
    ["/icon.jpg", ASSET_CACHE_CONTROL],
    ["/Logitrack-logo.jpg", ASSET_CACHE_CONTROL],
    ["/usa_flag_v2.svg", ASSET_CACHE_CONTROL],
    ["/fonts/Sarabun-Regular.ttf", ASSET_CACHE_CONTROL],
    ["/about.html", PAGE_CACHE_CONTROL],
    ["/app/logo.png", APP_CACHE_CONTROL],
  ])("Cache-Control for %s", async (pathname, want) => {
    expect(await cacheControlFor(pathname)).toBe(want);
  });

  it("matches the Hosting document rule and default asset cache, and keeps /app private", () => {
    expect(PAGE_CACHE_CONTROL).toBe("public, max-age=0, must-revalidate");
    expect(ASSET_CACHE_CONTROL).toBe("public, max-age=3600");
    expect(APP_CACHE_CONTROL).toBe("private, no-cache");
    expect(PAGE_SOURCE.startsWith("/:path(")).toBe(true);
  });

  it("serves every file of public/ with the asset rule", async () => {
    const files = [...walkFiles(path.join(webDir, "public"))].map((f) => "/" + path.relative(path.join(webDir, "public"), f).split(path.sep).join("/"));
    expect(files.length).toBeGreaterThan(0);
    for (const f of files) expect([f, await cacheControlFor(f)]).toEqual([f, ASSET_CACHE_CONTROL]);
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
    expect(Object.keys(hosting).sort()).toEqual(["ignore", "predeploy", "public", "redirects"]);
    // Any deploy that includes Hosting runs the guard first and aborts while WEB_DOMAIN is a placeholder.
    expect(hosting.predeploy).toEqual([`node "$PROJECT_DIR/scripts/check-hosting-redirect.mjs" "$PROJECT_DIR/${file}"`]);
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

  it.each(["firebase.json", "firebase.prod.json"])("the predeploy guard refuses %s as committed (placeholder host)", (file) => {
    const cfg = JSON.parse(fs.readFileSync(path.join(webDir, file), "utf8"));
    const problems = checkHostingRedirect(cfg);
    expect(problems).toHaveLength(1);
    expect(problems[0]).toMatch(/placeholder WEB_DOMAIN/);
  });

  it("the predeploy guard accepts a decided host and refuses anything else", () => {
    const withDest = (destination: string) => ({ hosting: { redirects: [{ source: "/:path*", destination, type: 301 }] } });
    expect(checkHostingRedirect(withDest("https://admin.example.com/:path"))).toEqual([]);
    expect(checkHostingRedirect(withDest("https://web_domain/:path"))).toHaveLength(1);
    expect(checkHostingRedirect(withDest("https://localhost/:path"))).toHaveLength(1);
    expect(checkHostingRedirect(withDest("http://admin.example.com/:path"))).toHaveLength(1);
    expect(checkHostingRedirect(withDest(""))).toHaveLength(1);
    expect(checkHostingRedirect({ hosting: { redirects: [] } })).toHaveLength(1);
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

  it("scans the prerendered pages the server returns as they are, not server code", () => {
    const web = fixture({});
    const write = (rel: string, body: string) => {
      fs.mkdirSync(path.dirname(path.join(web, rel)), { recursive: true });
      fs.writeFileSync(path.join(web, rel), body);
    };
    // A static server component rendering a server-only value bakes it into the prerender output.
    write(".next/server/app/leak.html", `<p>${env.GO_API_INTERNAL_URL}</p>`);
    write(".next/server/app/leak.segments/_full.segment.rsc", `0:["${env.GO_API_INTERNAL_URL}"]`);
    write(".next/server/pages/404.html", `<p>${env.GO_API_INTERNAL_URL}</p>`);
    // Server bundles are never sent to browsers; a runtime env read is not a leak.
    write(".next/server/app/leak/page.js", `const u = ${JSON.stringify(env.GO_API_INTERNAL_URL)};`);
    const problems: string[] = check({ env, webDir: web }).problems;
    expect(problems).toHaveLength(3);
    expect(problems.every((p) => /server-only GO_API_INTERNAL_URL found in browser-facing output/.test(p))).toBe(true);
    expect(problems.join("\n")).toMatch(/leak\.html/);
    expect(problems.join("\n")).toMatch(/_full\.segment\.rsc/);
    expect(problems.join("\n")).toMatch(/404\.html/);
  });

  // T17, R41: the browser holds no Go host; only server code reads GO_API_INTERNAL_URL.
  it("refuses a Go host or GO_API_INTERNAL_URL in browser code, and passes on the real tree", () => {
    const web = fixture({});
    const write = (rel: string, body: string) => {
      fs.mkdirSync(path.dirname(path.join(web, rel)), { recursive: true });
      fs.writeFileSync(path.join(web, rel), body);
    };
    write("lib/client.ts", 'export const u = "http://api:8080/v1/hubs";\n');
    write("features/x/api/useX.ts", "export const u = process.env.GO_API_INTERNAL_URL;\n");
    write("components/y.tsx", 'fetch("http://localhost:8081/v1/mobile/settings");\n');
    // IPv6 loopback: the character before `[` is `/`, so a leading word boundary would never match.
    write("lib/v6.ts", 'fetch("http://[::1]:8080/v1/hubs");\n');
    write("components/v6any.tsx", "fetch(`http://[::]:8081/v1/mobile/settings`);\n");
    // Look-alikes are not Go listeners.
    write("lib/lookalike.ts", 'export const u = ["http://myapi:8080/x", "http://localhost:80801/x", "http://[::2]:8080/x"];\n');
    // Server code may read it: route handlers, the edge gate and server-only modules.
    write("app/api/go/[...path]/route.ts", "const base = process.env.GO_API_INTERNAL_URL;\n");
    write("proxy.ts", "const jwks = new URL('/.well-known/jwks.json', process.env.GO_API_INTERNAL_URL);\n");
    write("lib/bff/upstream.ts", 'import "server-only";\nexport const base = process.env.GO_API_INTERNAL_URL;\n');
    // Tests may name anything.
    write("lib/goFetch.test.ts", 'const u = "http://api:8080";\n');
    const problems: string[] = checkSources({ webDir: web });
    expect(problems).toHaveLength(5);
    expect(problems.join("\n")).toMatch(/lib\/client\.ts names a Go listener/);
    expect(problems.join("\n")).toMatch(/components\/y\.tsx names a Go listener/);
    expect(problems.join("\n")).toMatch(/lib\/v6\.ts names a Go listener/);
    expect(problems.join("\n")).toMatch(/components\/v6any\.tsx names a Go listener/);
    expect(problems.join("\n")).not.toMatch(/lookalike/);
    expect(problems.join("\n")).toMatch(/features\/x\/api\/useX\.ts reads GO_API_INTERNAL_URL outside server code/);
    expect(check({ env, webDir: web }).problems).toEqual(problems);
    expect(checkSources({ webDir })).toEqual([]);
  });

  it("allow-lists exactly the §16.1 web-public names and covers every web-server value", () => {
    expect(NEXT_PUBLIC_ALLOW.size).toBe(11);
    expect(SERVER_ONLY).toContain("GO_API_INTERNAL_URL");
    expect(SERVER_ONLY).not.toContain("NODE_ENV");
  });
});

describe("scripts/start-standalone.mjs (pnpm start)", () => {
  function build() {
    const web = path.join(fs.mkdtempSync(path.join(os.tmpdir(), "start-standalone-")), "logitrack-web");
    const write = (rel: string, body: string) => {
      fs.mkdirSync(path.dirname(path.join(web, rel)), { recursive: true });
      fs.writeFileSync(path.join(web, rel), body);
    };
    write(".next/standalone/logitrack-web/server.js", "");
    write(".next/static/chunks/app-1.js", "chunk");
    write("public/logo.jpg", "jpg");
    write("public/fonts/a.ttf", "ttf");
    return { web, write };
  }
  const out = (web: string, rel: string) => path.join(web, ".next/standalone/logitrack-web", rel);

  it("copies .next/static and public next to server.js, as the Dockerfile does", () => {
    const { web } = build();
    expect(prepareStandalone({ webDir: web })).toBe(out(web, "server.js"));
    expect(fs.readFileSync(out(web, ".next/static/chunks/app-1.js"), "utf8")).toBe("chunk");
    expect(fs.readFileSync(out(web, "public/logo.jpg"), "utf8")).toBe("jpg");
    expect(fs.readFileSync(out(web, "public/fonts/a.ttf"), "utf8")).toBe("ttf");
  });

  it("replaces the copies on every start, so stale chunks do not pile up", () => {
    const { web, write } = build();
    prepareStandalone({ webDir: web });
    fs.rmSync(path.join(web, ".next/static/chunks/app-1.js"));
    write(".next/static/chunks/app-2.js", "chunk2");
    prepareStandalone({ webDir: web });
    expect(fs.existsSync(out(web, ".next/static/chunks/app-1.js"))).toBe(false);
    expect(fs.readFileSync(out(web, ".next/static/chunks/app-2.js"), "utf8")).toBe("chunk2");
  });

  it("refuses to start without a standalone build", () => {
    const { web } = build();
    fs.rmSync(out(web, "server.js"));
    expect(() => prepareStandalone({ webDir: web })).toThrow(/run pnpm build first/);
    const { web: noStatic } = build();
    fs.rmSync(path.join(noStatic, ".next/static"), { recursive: true });
    expect(() => prepareStandalone({ webDir: noStatic })).toThrow(/run pnpm build first/);
  });
});
