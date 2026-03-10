import type { NextConfig } from "next";
import path from "path";

const nextConfig: NextConfig = {
  turbopack: {
    root: __dirname,
  },
  output: process.env.NODE_ENV === "production" ? "export" : undefined,
  experimental:
    process.env.NODE_ENV === "production"
      ? { adapterPath: path.join(__dirname, "build", "adapter.js") }
      : undefined,
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
};

export default nextConfig;