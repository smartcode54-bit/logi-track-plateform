#!/usr/bin/env node
// `pnpm start`: runs the standalone server of the last `pnpm build` outside Docker
// (developer-spec.md §10.12, Appendix E §E.9.2; TW2).
//
// Next's standalone trace leaves out `.next/static` and `public/`, and server.js serves both from
// its own directory (it chdirs there). The image copies them next to server.js
// (logitrack-web/Dockerfile, runtime stage); this script does the same before starting, so pages
// get their JS, CSS and public files. The copies are replaced on every start, so stale hashed
// chunks of an earlier build do not pile up. PORT and HOSTNAME pass through from the environment.
import fs from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

const defaultWebDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

/**
 * Copies `.next/static` and `public` into the standalone output, as the Dockerfile does.
 * @param {{ webDir?: string }} [options]
 * @returns {string} the path of server.js
 */
export function prepareStandalone({ webDir = defaultWebDir } = {}) {
  const out = path.join(webDir, ".next", "standalone", "logitrack-web");
  const serverJs = path.join(out, "server.js");
  const staticDir = path.join(webDir, ".next", "static");
  for (const required of [serverJs, staticDir]) {
    if (!fs.existsSync(required)) {
      throw new Error(`missing ${path.relative(webDir, required)}: run pnpm build first (output "standalone")`);
    }
  }
  const copies = [
    [staticDir, path.join(out, ".next", "static")],
    [path.join(webDir, "public"), path.join(out, "public")],
  ];
  for (const [from, to] of copies) {
    fs.rmSync(to, { recursive: true, force: true });
    if (fs.existsSync(from)) fs.cpSync(from, to, { recursive: true });
  }
  return serverJs;
}

function main() {
  let serverJs;
  try {
    serverJs = prepareStandalone();
  } catch (err) {
    console.error(`start-standalone: ${err instanceof Error ? err.message : err}`);
    process.exit(1);
  }
  // server.js is CommonJS (the standalone package.json has no "type"); it chdirs to its own
  // directory and listens on PORT / HOSTNAME.
  createRequire(import.meta.url)(serverJs);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
