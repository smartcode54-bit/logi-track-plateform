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
  "jszip",
  "fullcalendar",
  "leaflet",
  "react-leaflet",
  "leaflet-geosearch",
];
const LAZY_MODULES = [
  "app/app/sources/pickup-import-dialog.tsx",
  "features/trucks/components/TruckImportDialog.tsx",
  "features/holidays/components/HolidayCalendar.tsx",
  "components/map/LocationPicker.tsx",
  "components/map/SourcesMap.tsx",
  // T54R: the Go dashboard's map client (leaflet), loaded by FleetMap through next/dynamic.
  "features/dashboard/components/FleetMapClient.tsx",
  "features/incident-reports/components/IncidentLocationMapClient.tsx",
  // TW8 / UI2: the virtualised list of a long searchable select (@tanstack/react-virtual) loads on demand
  // (FilterCombobox is a SearchSelect since UI2).
  "components/ui/search-select-virtual.tsx",
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

// TW7 (issue #87, developer-spec.md §10.14 #8): the web holds no Firebase SDK. Pages read and write
// through the BFF (`lib/goFetch.ts` -> `/api/go/v1/*`), uploads go to Go's presigned or local upload
// routes and the web sends no push, so neither Storage nor FCM needs the client SDK. Type imports are
// refused too (the package is not installed). `firebase-admin` is not matched: it is a devDependency
// of the operator scripts under scripts/ (ignored here) and of functions/.
const FIREBASE_MESSAGE =
  "The web has no Firebase SDK since TW7 (developer-spec.md §10.13, §10.14 #8): read and write through lib/goFetch.ts and the BFF (/api/go/v1/*).";
// F87 (issue #87, R69, Appendix E §E.7 row 15): Go renders every billing document, so the browser
// document stack is gone from package.json; any import of it, lazy modules included, is refused.
const DOCUMENT_STACK_MESSAGE =
  "The browser document stack was removed (F87): Go renders the statement documents and the Shopee report (R69, developer-spec.md §10.13).";
const BANNED_PATHS = [
  { name: "firebase", message: FIREBASE_MESSAGE },
  ...["jspdf", "jspdf-autotable", "xlsx-js-style", "bahttext"].map((name) => ({ name, message: DOCUMENT_STACK_MESSAGE })),
];
const FIREBASE_PATTERNS = [{ group: ["firebase/*", "@firebase/*", "@/firebase", "@/firebase/*"], message: FIREBASE_MESSAGE }];

// UI2 (issue #142): every pick-from-a-list control is the shared searchable select
// (components/ui/search-select.tsx, or its compound API components/ui/select.tsx). Radix Select is not a
// dependency any more, and a native <select> is refused outside components/ui (lib/selectPrimitives.test.ts
// checks the same on the source tree).
const SELECT_MESSAGE =
  "Use the shared searchable select (components/ui/search-select.tsx, or the compound Select of components/ui/select.tsx), UI2.";
const SELECT_PATHS = [{ name: "@radix-ui/react-select", message: SELECT_MESSAGE }];

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
          paths: [
            ...HEAVY_LIBRARIES.map((name) => ({ name, allowTypeImports: true, message: HEAVY_LIBRARY_MESSAGE })),
            ...BANNED_PATHS,
            ...SELECT_PATHS,
          ],
          patterns: [
            { group: ["@fullcalendar/*"], allowTypeImports: true, message: HEAVY_LIBRARY_MESSAGE },
            LAZY_MODULE_IMPORT,
            ...FIREBASE_PATTERNS,
          ],
        },
      ],
    },
  },
  {
    // The lazily loaded modules themselves may import the heavy libraries.
    files: LAZY_MODULES,
    rules: {
      "@typescript-eslint/no-restricted-imports": [
        "error",
        { paths: [...BANNED_PATHS, ...SELECT_PATHS], patterns: [LAZY_MODULE_IMPORT, ...FIREBASE_PATTERNS] },
      ],
    },
  },
  {
    files: ["**/*.tsx", "**/*.jsx"],
    ignores: ["components/ui/**"],
    rules: {
      "react/forbid-elements": ["error", { forbid: [{ element: "select", message: SELECT_MESSAGE }] }],
    },
  },
]);

export default eslintConfig;
