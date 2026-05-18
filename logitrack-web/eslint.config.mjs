import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  // Override default ignores of eslint-config-next.
  globalIgnores([
    // Default ignores of eslint-config-next:
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
    // Node.js utility scripts using CommonJS
    "fix-imports.js",
    "refactor-paths.js",
    "scripts/**",
    // Firebase Functions compiled output and Node.js scripts
    "functions/lib/**",
    "functions/scripts/**",
    "functions/run-backfill.js",
  ]),
  {
    rules: {
      // Downgrade to warn — codebase has widespread any usage, treat as tech debt not blocker
      "@typescript-eslint/no-explicit-any": "warn",
    },
  },
]);

export default eslintConfig;
