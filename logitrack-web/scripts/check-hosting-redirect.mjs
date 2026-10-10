#!/usr/bin/env node
// Hosting predeploy guard (developer-spec.md §10.12, Appendix E §E.9.3; TW2).
//
// Until P8 Firebase Hosting serves only a 301 to the web on WEB_DOMAIN. firebase.json and
// firebase.prod.json carry the literal placeholder host `WEB_DOMAIN` until the owner writes the
// decided host over it in the commit used for the targeted Hosting deploy (§19 questions 8, 18).
// Both files run this script as `hosting.predeploy`, and the Firebase CLI runs every predeploy hook
// before anything is prepared or released, so a deploy that includes Hosting (`firebase deploy`,
// `--only hosting,functions,firestore`, ...) aborts as a whole while the placeholder is there. A
// browser caches a 301, so a redirect to a host that does not resolve could not be taken back.
// Targeted `--only functions:<names>` / `--only firestore:rules` deploys do not run it.
//
// Usage: node scripts/check-hosting-redirect.mjs <firebase config file>
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const PLACEHOLDER_HOST = "WEB_DOMAIN";

/**
 * @param {{ hosting?: { redirects?: Array<{ destination?: string }> } }} config parsed firebase.json
 * @returns {string[]} problems; empty when every redirect points at a real https host
 */
export function checkHostingRedirect(config) {
  const problems = [];
  const redirects = config?.hosting?.redirects ?? [];
  if (redirects.length === 0) problems.push("hosting.redirects is empty (Hosting must redirect to WEB_DOMAIN until P8)");
  for (const [i, r] of redirects.entries()) {
    const where = `hosting.redirects[${i}].destination`;
    const dest = typeof r?.destination === "string" ? r.destination : "";
    if (dest.includes(PLACEHOLDER_HOST)) {
      problems.push(
        `${where} still holds the placeholder ${PLACEHOLDER_HOST}: write the decided host over it ` +
          "in the commit used for the targeted Hosting deploy (developer-spec.md §10.12, §19 questions 8 and 18)",
      );
      continue;
    }
    let host = "";
    try {
      const u = new URL(dest.replace("/:path", "/"));
      host = u.protocol === "https:" ? u.hostname : "";
    } catch {
      host = "";
    }
    if (!host.includes(".") || host.startsWith(".") || host.endsWith(".")) {
      problems.push(`${where} must be https:// plus a fully qualified host name`);
    }
  }
  return problems;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const file = process.argv[2];
  if (!file) {
    console.error("usage: node scripts/check-hosting-redirect.mjs <firebase config file>");
    process.exit(2);
  }
  const problems = checkHostingRedirect(JSON.parse(fs.readFileSync(file, "utf8")));
  if (problems.length > 0) {
    for (const p of problems) console.error(`check-hosting-redirect: ${path.basename(file)}: ${p}`);
    console.error("check-hosting-redirect: Hosting deploy refused");
    process.exit(1);
  }
  console.log(`check-hosting-redirect: ${path.basename(file)} ok`);
}
