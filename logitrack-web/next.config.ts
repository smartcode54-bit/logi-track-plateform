import bundleAnalyzer from "@next/bundle-analyzer";
import type { NextConfig } from "next";
import path from "path";

const nextConfig: NextConfig = {
  transpilePackages: ["shared-docs"],
  turbopack: {
    // Must stay logitrack-web so shared-docs (and other workspace deps) resolve from logitrack-web/node_modules
    root: path.resolve(__dirname),
  },
  output: process.env.NODE_ENV === "production" ? "export" : undefined,
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