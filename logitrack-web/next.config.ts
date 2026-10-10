import bundleAnalyzer from "@next/bundle-analyzer";
import type { NextConfig } from "next";
import path from "path";

/**
 * Headers shared by every response (developer-spec.md §10.12, Appendix E §E.9.1).
 * `Cross-Origin-Embedder-Policy` carries over the Hosting rule `**` of firebase.json;
 * the other two are hardening added with the move to a Node server.
 */
export const SECURITY_HEADERS = [
  { key: "Cross-Origin-Embedder-Policy", value: "unsafe-none" },
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
];

/**
 * Cache rules carried over from Firebase Hosting (firebase.json:34-61 before TW2):
 * - `/_next/static/*`: Next's own `public, max-age=31536000, immutable` (hashed assets), not set here;
 * - documents (`**\/*.html` on Hosting) must revalidate on every load: `PAGE_CACHE_CONTROL` on
 *   `PAGE_SOURCE`, every path outside `/_next`, `/api` and `/app`;
 * - unhashed images and fonts (`public/`, `app/icon.jpg`) matched no Hosting header rule and got
 *   Hosting's default one-hour cache: `ASSET_CACHE_CONTROL` on `ASSET_SOURCE`, a later rule than
 *   `PAGE_SOURCE`, so it wins (Next applies matching rules in order, the last value of a key wins);
 * - `/app/*` documents are per-user from TW3 on (proxy.ts gate), so no shared cache may keep
 *   them either: `APP_CACHE_CONTROL` (developer-spec.md §10.12).
 * Verified on the standalone build of Next 16.1.1 (TW2): headers() values reach the browser on
 * prerendered and dynamic pages, RSC payloads and `public/` files; Next does not override them,
 * so Caddy leaves Cache-Control alone. `/api/*` route handlers set their own.
 */
export const PAGE_CACHE_CONTROL = "public, max-age=0, must-revalidate";
export const APP_CACHE_CONTROL = "private, no-cache";
export const ASSET_CACHE_CONTROL = "public, max-age=3600";

/** Every path except `/_next/*`, `/api/*` and `/app`, `/app/*` (path-to-regexp negative lookahead). */
export const PAGE_SOURCE = "/:path((?!_next/|api/|app/|app$).*)";

/** Image and font files outside `/_next/*`, `/api/*` and `/app/*`: `public/` and metadata files. */
export const ASSET_EXTENSIONS = ["jpg", "jpeg", "png", "gif", "svg", "ico", "webp", "avif", "ttf", "otf", "woff", "woff2"];
export const ASSET_SOURCE = `/:path((?!_next/|api/|app/).+\\.(?:${ASSET_EXTENSIONS.join("|")}))`;

const monorepoRoot = path.join(__dirname, "..");

const nextConfig: NextConfig = {
  // W1 (TW2): always a Node server — the `web` container behind Caddy. The static export
  // (`output: "export"` in production) and its Firebase Hosting placeholder rewrites are gone.
  output: "standalone",
  // Trace from the monorepo root so workspace packages (shared-docs) resolve; server.js then
  // lands at .next/standalone/logitrack-web/server.js.
  outputFileTracingRoot: monorepoRoot,
  // Caddy compresses. Next's own gzip would buffer /api/go/v1/events (SSE, Appendix E §E.8.5).
  compress: false,
  transpilePackages: ["shared-docs"],
  turbopack: {
    // Next requires turbopack.root == outputFileTracingRoot (it warns and uses the latter otherwise).
    // Turbopack is not used: `dev` and `build` both pass --webpack, where the alias below resolves
    // shared-docs.
    root: monorepoRoot,
  },
  images: {
    unoptimized: true,
    remotePatterns: [
      {
        protocol: "https",
        hostname: "lh3.googleusercontent.com",
      },
      {
        protocol: "https",
        hostname: "firebasestorage.googleapis.com",
        pathname: "/**",
      },
      {
        protocol: "https",
        hostname: "flagcdn.com",
        pathname: "/**",
      },
      {
        protocol: "https",
        hostname: "images.unsplash.com",
        pathname: "/**",
      },
    ],
  },
  async headers() {
    return [
      { source: "/:path*", headers: SECURITY_HEADERS },
      { source: PAGE_SOURCE, headers: [{ key: "Cache-Control", value: PAGE_CACHE_CONTROL }] },
      { source: ASSET_SOURCE, headers: [{ key: "Cache-Control", value: ASSET_CACHE_CONTROL }] },
      // `/app/:path*` also matches `/app` itself.
      { source: "/app/:path*", headers: [{ key: "Cache-Control", value: APP_CACHE_CONTROL }] },
    ];
  },
  async redirects() {
    // Legacy page redirects become HTTP redirects (no client round-trip); the pages stay as fallback.
    return [
      { source: "/app/users", destination: "/app/security-center/users", permanent: true },
      { source: "/app/operations/roles", destination: "/app/security-center/roles", permanent: true },
    ];
  },
  webpack: (config, { isServer }) => {
    // Resolve shared-docs from monorepo sibling (pnpm workspace resolution can fail in Next)
    config.resolve ??= {};
    config.resolve.alias = {
      ...config.resolve.alias,
      "shared-docs": path.resolve(__dirname, "../shared-docs"),
    };
    return config;
  },
};
// Bundle analysis (TW9, developer-spec.md §10.11): `pnpm analyze` sets ANALYZE=true and writes
// .next/analyze/{client,nodejs,edge}.html for people plus the same data as .json, which
// scripts/bundle-report.mjs turns into the per-route report and the bundle gate. It swaps in a
// wrapped `webpack` so the exported config object stays the same; the shallow copy keeps the
// wrapper chain pointing at the original function.
if (process.env.ANALYZE === "true") {
  const html = bundleAnalyzer({ enabled: true, openAnalyzer: false, logLevel: "warn" });
  const json = bundleAnalyzer({ enabled: true, openAnalyzer: false, logLevel: "warn", analyzerMode: "json" });
  nextConfig.webpack = html(json({ ...nextConfig })).webpack;
}

export default nextConfig;
