import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

// Bundle guard (TW9, developer-spec.md §10.11 and Appendix E §E.7). Heavy browser-only libraries
// load lazily: they may be imported statically only inside the modules below, and those modules
// may only be loaded with `next/dynamic` or `await import()` — a static import, or a barrel
// re-exporting one, would put the library back into a page's initial JS. Type imports are fine.
// `pnpm analyze && pnpm bundle:report --check` measures the same thing on the built output.
const HEAVY_LIBRARIES = [
  "xlsx",
  "xlsx-js-style",
  "jspdf",
  "jspdf-autotable",
  "bahttext",
  "jszip",
  "fullcalendar",
  "leaflet",
  "react-leaflet",
  "leaflet-geosearch",
];
const LAZY_MODULES = [
  "app/app/first-mile/import-dialog.tsx",
  "app/app/line-haul/import-dialog.tsx",
  "app/app/job-assign/import-dialog.tsx",
  "app/app/sources/pickup-import-dialog.tsx",
  "features/trucks/components/TruckImportDialog.tsx",
  "features/accounting/components/RateCardImportDialog.tsx",
  "features/accounting/components/TollExpenseImportDialog.tsx",
  "features/holidays/components/HolidayCalendar.tsx",
  "lib/billingDocumentRender.ts",
  "lib/shopeeExpressReport.ts",
  "components/map/LocationPicker.tsx",
  "components/map/SourcesMap.tsx",
  "features/security-center/components/SessionLoginLocationMap.tsx",
  "features/dashboard/components/DashboardVehicleMapClient.tsx",
  "features/incident-reports/components/IncidentLocationMapClient.tsx",
];
const lazyModuleName = (file) => file.split("/").pop().replace(/\.tsx?$/, "");
const LAZY_MODULE_IMPORT = {
  regex: `(^|/)(${[...new Set(LAZY_MODULES.map(lazyModuleName))].join("|")})$`,
  allowTypeImports: true,
  message:
    "Load this module with next/dynamic or await import() (developer-spec.md §10.11); a static import or re-export puts it and the library it wraps into the initial JS.",
};
const HEAVY_LIBRARY_MESSAGE =
  "Heavy library: import it inside a lazily loaded module (see LAZY_MODULES in eslint.config.mjs) or with await import() in a handler (developer-spec.md §10.11).";

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
      // Tech debt — widespread any usage, fix incrementally per file
      "@typescript-eslint/no-explicit-any": "warn",
      // Tech debt — React Compiler rules, pattern works but violates newer strict rules
      "react-hooks/set-state-in-effect": "warn",
      "react-hooks/preserve-manual-memoization": "warn",
    },
  },
  {
    files: ["**/*.ts", "**/*.tsx"],
    ignores: ["types/**", "**/*.d.ts"],
    rules: {
      "@typescript-eslint/no-restricted-imports": [
        "error",
        {
          paths: HEAVY_LIBRARIES.map((name) => ({ name, allowTypeImports: true, message: HEAVY_LIBRARY_MESSAGE })),
          patterns: [
            { group: ["@fullcalendar/*"], allowTypeImports: true, message: HEAVY_LIBRARY_MESSAGE },
            LAZY_MODULE_IMPORT,
          ],
        },
      ],
    },
  },
  {
    // The lazily loaded modules themselves may import the heavy libraries.
    files: LAZY_MODULES,
    rules: {
      "@typescript-eslint/no-restricted-imports": ["error", { patterns: [LAZY_MODULE_IMPORT] }],
    },
  },
]);

export default eslintConfig;
