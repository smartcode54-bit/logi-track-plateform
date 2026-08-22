#!/usr/bin/env node
/**
 * Pre-generate `functions/functions.yaml` (the Cloud Functions backend manifest).
 *
 * WHY THIS EXISTS
 * ---------------
 * `firebase deploy` normally discovers the backend spec by spawning
 * `node_modules/.bin/firebase-functions` (an extension-less shell shim) and polling
 * `http://127.0.0.1:<port>/__/functions.yaml` for 10 seconds.
 *
 * On Windows with Node >= 20.12 that spawn fails immediately:
 *   - extension-less shim  -> ENOENT (libuv no longer resolves it to the .cmd twin)
 *   - explicit .cmd        -> EINVAL (Node's CVE-2024-27980 guard: .cmd/.bat need shell: true)
 *
 * firebase-tools spawns without `shell`, and never listens for the child's `error`
 * event on that code path, so the failure is swallowed and the CLI just polls a dead
 * port until it reports:
 *
 *   "User code failed to load. Cannot determine backend specification. Timeout after 10000."
 *
 * The CLI checks for a pre-generated `functions.yaml` in the source dir FIRST
 * (`detectFromYaml`) and skips the spawn entirely when it finds one. So we produce that
 * file ourselves by invoking the SDK entry point with `node` directly — no shell shim,
 * no port, no 10s race.
 *
 * The manifest is project-specific (v1 auth triggers embed `projects/<id>`), so it is
 * regenerated on every deploy from `$GCLOUD_PROJECT`, which the firebase predeploy hook
 * sets for us. It is gitignored — never commit it, never hand-edit it.
 */
const { spawnSync } = require("child_process");
const fs = require("fs");
const path = require("path");

const functionsDir = path.resolve(__dirname, "..");
const outputPath = path.join(functionsDir, "functions.yaml");
const sdkEntry = path.join(
    functionsDir,
    "node_modules",
    "firebase-functions",
    "lib",
    "bin",
    "firebase-functions.js"
);

function fail(message) {
    console.error(`[generate-manifest] ${message}`);
    process.exit(1);
}

// Delete first, before any validation: a manifest left over from an earlier run (possibly for a
// different project) must never survive a failed regeneration. Fail closed.
fs.rmSync(outputPath, { force: true });

const projectId = process.env.GCLOUD_PROJECT || process.env.FIREBASE_PROJECT || "";
if (!projectId) {
    fail(
        "GCLOUD_PROJECT is not set.\n" +
            "  firebase deploy sets it for predeploy hooks. To run this by hand:\n" +
            "    GCLOUD_PROJECT=logi-track-wrt-dev npm run discover   (or $env:GCLOUD_PROJECT in PowerShell)"
    );
}

if (!fs.existsSync(sdkEntry)) {
    fail(`Cannot find the firebase-functions SDK at ${sdkEntry}. Run \`npm ci\` in functions/ first.`);
}

const result = spawnSync(process.execPath, [sdkEntry, "."], {
    cwd: functionsDir,
    stdio: "inherit",
    env: {
        ...process.env,
        GCLOUD_PROJECT: projectId,
        FIREBASE_CONFIG: process.env.FIREBASE_CONFIG || JSON.stringify({ projectId }),
        FUNCTIONS_CONTROL_API: "true",
        FUNCTIONS_MANIFEST_OUTPUT_PATH: outputPath,
    },
});

if (result.error) {
    fail(`Failed to run the functions SDK: ${result.error.message}`);
}
if (result.status !== 0) {
    fail(`Functions SDK exited with code ${result.status} — the manifest was not written.`);
}
if (!fs.existsSync(outputPath)) {
    fail("Functions SDK reported success but no manifest was written.");
}

let manifest;
try {
    manifest = JSON.parse(fs.readFileSync(outputPath, "utf8"));
} catch (e) {
    fs.rmSync(outputPath, { force: true });
    fail(`Manifest is not parseable, removed it: ${e instanceof Error ? e.message : String(e)}`);
}

const endpoints = Object.keys(manifest.endpoints || {});
if (endpoints.length === 0) {
    fs.rmSync(outputPath, { force: true });
    fail("Manifest contains 0 endpoints — refusing to deploy an empty backend spec.");
}

console.log(
    `[generate-manifest] Wrote functions.yaml for '${projectId}' — ${endpoints.length} endpoints, ` +
        `spec ${manifest.specVersion}`
);
