/**
 * Next.js static export places RSC flight files as nested paths, e.g.
 *   out/admin/chat/__next.admin/chat.txt
 * The client requests flat dot-separated names, e.g.
 *   /admin/chat/__next.admin.chat.txt
 * Firebase Hosting does not map dots to folders, so those requests 404.
 * This script copies nested `__next.*` .txt files to the sibling flat filename.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const outDir = path.join(__dirname, "..", "out");

function walk(dir, baseRel = "") {
    const entries = fs.readdirSync(dir, { withFileTypes: true });
    for (const ent of entries) {
        const rel = baseRel ? `${baseRel}/${ent.name}` : ent.name;
        const full = path.join(dir, ent.name);
        if (ent.isDirectory()) {
            walk(full, rel);
        } else if (ent.name.endsWith(".txt")) {
            maybeFlatten(rel, full);
        }
    }
}

function maybeFlatten(relPosix, absFile) {
    const parts = relPosix.split("/");
    const i = parts.findIndex((p) => p.startsWith("__next"));
    if (i === -1) return;
    const nested = parts.slice(i);
    if (nested.length < 2) return;

    const flatName = nested.join(".");
    const destRel = [...parts.slice(0, i), flatName].join("/");
    const destAbs = path.join(outDir, ...destRel.split("/"));

    fs.mkdirSync(path.dirname(destAbs), { recursive: true });
    fs.copyFileSync(absFile, destAbs);
}

if (!fs.existsSync(outDir)) {
    console.error("flatten-next-flight-paths: missing out/ — run next build first");
    process.exit(1);
}

walk(outDir);
console.log("flatten-next-flight-paths: synced nested __next.*.txt → dot-separated names");
