import { defineConfig } from "vitest/config";
import path from "path";

export default defineConfig({
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./vitest.setup.ts"],
    include: ["lib/**/*.test.ts", "components/**/*.test.tsx", "**/__tests__/**/*.test.{ts,tsx}", "**/*.test.{ts,tsx}"],
    exclude: ["node_modules", "functions/node_modules", ".next", "out", "tests/**/*.spec.ts"],
  },
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "."),
      // Next aliases `server-only` itself (an empty module on the server layers, an error on the
      // client); tests import the BFF modules on the server side.
      "server-only": path.resolve(__dirname, "node_modules/next/dist/compiled/server-only/empty.js"),
    },
  },
});
