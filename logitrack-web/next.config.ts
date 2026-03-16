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

export default nextConfig;