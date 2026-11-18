#!/usr/bin/env node
// Builds the mv-go test APK (flavor `gotest`, package com.wrt.logitrack.gotest).
//
// It installs next to the fleet's prod app and never replaces it, runs on Firebase dev and talks to the Go
// staging API. Every check below fails the build rather than risk a test APK that reaches prod:
//   - API_BASE_URL and GOTEST_BUILD come from envs/.env.gotest.mobile (gitignored; see the .example);
//   - android/app/src/gotest/google-services.json must exist, name this package, and not be the prod project;
//   - envs/.env.dev.mobile (copied to logitrack-mobile/.env.dev, which gotest loads) must not be the prod project;
//   - pubspec.yaml bundles .env.prod as an asset in every flavor: for this build it is replaced by an empty file
//     (the developer's own .env.prod is put back afterwards, whatever happens), so the test APK carries no prod key.
// The version is 4.0.0-gotest.<GOTEST_BUILD>: never published (no publish-mobile-release.mjs), and below 4.0.0
// for pub_semver, so a later real 4.0.0 floor blocks it.
//
// Usage (repo root): pnpm build:mobile:gotest
import { spawnSync } from "node:child_process";
import { copyFileSync, existsSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const mobile = join(root, "logitrack-mobile");
const PACKAGE = "com.wrt.logitrack.gotest";

function fail(msg) {
    console.error(`[build-mobile-gotest] ${msg}`);
    process.exit(1);
}

function readEnv(path) {
    if (!existsSync(path)) fail(`missing ${path}`);
    const out = {};
    for (const line of readFileSync(path, "utf8").split(/\r?\n/)) {
        const m = /^\s*([A-Z0-9_]+)\s*=\s*(.*?)\s*$/.exec(line);
        if (m) out[m[1]] = m[2].replace(/^["']|["']$/g, "");
    }
    return out;
}

function googleServices(flavor) {
    const path = join(mobile, "android", "app", "src", flavor, "google-services.json");
    if (!existsSync(path)) fail(`missing ${path}`);
    const json = JSON.parse(readFileSync(path, "utf8"));
    return {
        projectId: json.project_info?.project_id,
        packages: (json.client ?? []).map((c) => c.client_info?.android_client_info?.package_name),
    };
}

const cfg = readEnv(join(root, "envs", ".env.gotest.mobile"));
const apiBaseUrl = cfg.API_BASE_URL ?? "";
if (!/^https?:\/\/\S+$/.test(apiBaseUrl)) fail("API_BASE_URL in envs/.env.gotest.mobile must be an http(s) URL");
const build = Number(cfg.GOTEST_BUILD);
if (!Number.isInteger(build) || build < 1) fail("GOTEST_BUILD in envs/.env.gotest.mobile must be a positive integer");

const prodProject = googleServices("prod").projectId;
const gotest = googleServices("gotest");
if (!gotest.packages.includes(PACKAGE)) fail(`src/gotest/google-services.json has no client for ${PACKAGE}`);
if (gotest.projectId === prodProject) fail(`src/gotest/google-services.json is the prod project (${prodProject})`);

const devEnvPath = join(root, "envs", ".env.dev.mobile");
const devEnv = readEnv(devEnvPath);
if (devEnv.FIREBASE_PROJECT_ID === prodProject) fail(`envs/.env.dev.mobile is the prod project (${prodProject})`);
if (devEnv.FIREBASE_PROJECT_ID !== gotest.projectId) {
    fail(`envs/.env.dev.mobile (${devEnv.FIREBASE_PROJECT_ID}) and src/gotest/google-services.json ` +
        `(${gotest.projectId}) name different Firebase projects`);
}
copyFileSync(devEnvPath, join(mobile, ".env.dev"));

const buildName = `4.0.0-gotest.${build}`;
const args = [
    "build", "apk", "--release",
    "--flavor", "gotest",
    "--dart-define=FLAVOR=gotest",
    `--dart-define=API_BASE_URL=${apiBaseUrl}`,
    `--build-name=${buildName}`,
    `--build-number=${build}`,
];
console.log(`[build-mobile-gotest] ${PACKAGE} ${buildName} -> ${apiBaseUrl} (Firebase ${gotest.projectId})`);
const prodEnv = join(mobile, ".env.prod");
const prodEnvBackup = join(mobile, ".env.prod.gotest-build-backup");
if (existsSync(prodEnvBackup)) fail(`${prodEnvBackup} exists: a previous build was interrupted; restore it to .env.prod first`);
const hadProdEnv = existsSync(prodEnv);
if (hadProdEnv) renameSync(prodEnv, prodEnvBackup);
let status = 1;
try {
    writeFileSync(prodEnv, "# gotest build: no prod values in this APK\n");
    const res = spawnSync("flutter", args, { cwd: mobile, stdio: "inherit", shell: process.platform === "win32" });
    status = res.status ?? 1;
} finally {
    rmSync(prodEnv, { force: true });
    if (hadProdEnv) renameSync(prodEnvBackup, prodEnv);
}
process.exit(status);
