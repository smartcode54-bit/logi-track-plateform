// UI2 (issue #142) AC: no Radix <Select> and no native <select> remain outside components/ui. Every
// pick-from-a-list control is the shared searchable select (components/ui/search-select.tsx, its compound
// front end components/ui/select.tsx, the pickers of components/entity-pickers.tsx). The same rules run in
// `pnpm lint` (eslint.config.mjs: `@radix-ui/react-select` restricted, `react/forbid-elements` select).
import { existsSync, readdirSync, readFileSync, statSync } from "fs";
import path from "path";
import { describe, expect, it } from "vitest";

const root = path.resolve(__dirname, "..");
const SOURCE_DIRS = ["app", "components", "context", "features", "hooks", "lib", "types", "validate", "test-utils"];
const THIS_FILE = path.relative(root, __filename);

function walk(dir: string, out: string[] = []): string[] {
    if (!existsSync(dir)) return out;
    for (const name of readdirSync(dir)) {
        const p = path.join(dir, name);
        if (statSync(p).isDirectory()) {
            if (name !== "node_modules" && name !== ".next") walk(p, out);
        } else if (/\.(m|c)?(j|t)sx?$/.test(name)) out.push(p);
    }
    return out;
}

const files = SOURCE_DIRS.flatMap((d) => walk(path.join(root, d)))
    .map((f) => ({ rel: path.relative(root, f).split(path.sep).join("/"), src: readFileSync(f, "utf8") }))
    .filter((f) => f.rel !== THIS_FILE);
const outsideUi = files.filter((f) => !f.rel.startsWith("components/ui/"));

describe("one searchable select (UI2)", () => {
    it("depends on no Radix Select", () => {
        const pkg = JSON.parse(readFileSync(path.join(root, "package.json"), "utf8")) as Record<string, Record<string, string> | undefined>;
        for (const field of ["dependencies", "devDependencies", "optionalDependencies"]) {
            expect(Object.keys(pkg[field] ?? {}).filter((n) => n === "@radix-ui/react-select"), field).toEqual([]);
        }
    });

    it("no file imports Radix Select", () => {
        expect(files.filter((f) => /["']@radix-ui\/react-select["']/.test(f.src)).map((f) => f.rel)).toEqual([]);
    });

    it("no native <select> outside components/ui", () => {
        expect(outsideUi.filter((f) => /\.tsx$/.test(f.rel) && /<select[\s>]/.test(f.src)).map((f) => f.rel)).toEqual([]);
    });

    it("cmdk is used only by the ui primitives (one list implementation)", () => {
        expect(outsideUi.filter((f) => /from\s+["']cmdk["']|["']@\/components\/ui\/command["']/.test(f.src)).map((f) => f.rel)).toEqual([]);
    });

    it("the removed one-off pickers stay removed", () => {
        for (const gone of ["components/ui/combobox.tsx", "components/ui/searchable-select.tsx", "components/filter-combobox-virtual-options.tsx"]) {
            expect(existsSync(path.join(root, gone)), gone).toBe(false);
        }
        expect(files.filter((f) => /["']@\/components\/ui\/(combobox|searchable-select)["']/.test(f.src)).map((f) => f.rel)).toEqual([]);
    });
});
