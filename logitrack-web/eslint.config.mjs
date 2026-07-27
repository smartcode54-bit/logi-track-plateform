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
    // Vendored AI agent tooling (BMAD/WDS skills, templates) — not app code,
    // ships CommonJS + its own rule set. Linting it only breaks CI.
    "_bmad/**",
    ".claude/**",
    ".agents/**",
  ]),
  {
    rules: {
      // Tech debt — widespread any usage, fix incrementally per file
      "@typescript-eslint/no-explicit-any": "warn",
      // Tech debt — React Compiler rules, pattern works but violates newer strict rules
      "react-hooks/set-state-in-effect": "warn",
      "react-hooks/preserve-manual-memoization": "warn",
    },
  },
]);

export default eslintConfig;
