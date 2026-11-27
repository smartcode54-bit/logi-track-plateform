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
// Usage (repo root):
//   pnpm build:mobile:gotest           APK for the Go staging API (API_BASE_URL)
//   pnpm build:mobile:gotest:local     APK for the local Go stack (logitrack-api `make up`), version
//                                      4.0.0-gotest.<N>.local, then `adb reverse` + `adb install -r` on the one
//                                      connected device. The phone calls http://127.0.0.1:<port> and adb carries it
//                                      to this machine; src/gotest/res/xml/network_security_config.xml allows plain
//                                      http to loopback only. The API URL is LOCAL_API_BASE_URL (default
//                                      http://127.0.0.1:18081, the forwarder container logitrack-api-fwd-public, because
//                                      the host port 8081 hangs on this machine); MinIO's presigned URLs use
//                                      localhost:9000, so 9000 is reversed as well.
//   pnpm gotest:reverse                only redo the adb reverse (it is lost when the phone is unplugged)
//   pnpm build:mobile:gotest:upgrade   install-over test of the 3.x -> 4.x session exchange (spec mobile-auth-exchange):
//                                      the gotest code under the DEV package (com.example.logi_track_driver_app, Firebase
//                                      dev, versionCode 400000+N, 4.0.0-gotest.<N>.upgrade), installed over a signed-in dev
//                                      3.x app; needs the local Go stack in AUTH_FIREBASE_BRIDGE_MODE=mobile
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
        // OAuth web clients (client_type 3): the `aud` of the ID token google_sign_in asks for (serverClientId).
        webClientIds: (json.client ?? []).flatMap((c) =>
            (c.oauth_client ?? []).filter((o) => o.client_type === 3).map((o) => o.client_id)),
    };
}

const argv = process.argv.slice(2);
const local = argv.includes("--local");
const reverseOnly = argv.includes("--reverse");
// Install-over test of the 3.x -> 4.x session exchange (spec mobile-auth-exchange R17-R19): the gotest code built
// under the DEV package and Firebase dev project, installed over a signed-in dev 3.x app. Local stack only.
const upgradeTest = argv.includes("--upgrade-test");
const unknown = argv.filter((a) => a !== "--local" && a !== "--reverse" && a !== "--upgrade-test");
if (unknown.length) fail(`unknown argument(s): ${unknown.join(" ")} (use --local, --reverse or --local --upgrade-test)`);
if (upgradeTest && (!local || reverseOnly)) fail("--upgrade-test needs --local (the local Go stack in bridge mode mobile)");
const FLAVOR = upgradeTest ? "dev" : "gotest";
const DEV_PACKAGE = "com.example.logi_track_driver_app";
const TARGET_PACKAGE = upgradeTest ? DEV_PACKAGE : PACKAGE;

// The hosts src/gotest/res/xml/network_security_config.xml lets through over plain http.
const LOOPBACK = new Set(["localhost", "127.0.0.1", "10.0.2.2"]);
const MINIO_PORT = 9000;

function adbPath() {
    const sdks = [process.env.ANDROID_HOME, process.env.ANDROID_SDK_ROOT];
    const props = join(mobile, "android", "local.properties");
    if (existsSync(props)) {
        const m = /^sdk\.dir=(.*)$/m.exec(readFileSync(props, "utf8"));
        if (m) sdks.unshift(m[1].trim().replace(/\\\\/g, "\\").replace(/\\:/g, ":"));
    }
    for (const sdk of sdks.filter(Boolean)) {
        const p = join(sdk, "platform-tools", process.platform === "win32" ? "adb.exe" : "adb");
        if (existsSync(p)) return p;
    }
    return "adb";
}

function adb(...args) {
    const res = spawnSync(adbPath(), args, { encoding: "utf8" });
    if (res.error) fail(`adb not found (${res.error.message}); install Android platform-tools`);
    return res;
}

/** Reverses the API and MinIO ports to this machine on the one connected device; false when there is none. */
function reversePorts(apiUrl) {
    const devices = adb("devices").stdout.split(/\r?\n/).slice(1).filter((l) => /\tdevice$/.test(l));
    if (devices.length !== 1) {
        console.error(`[build-mobile-gotest] ${devices.length} device(s) connected (need exactly 1, USB debugging on); ` +
            "connect the phone and run: pnpm gotest:reverse");
        return false;
    }
    const apiPort = new URL(apiUrl).port || "80";
    for (const port of [apiPort, String(MINIO_PORT)]) {
        const r = adb("reverse", `tcp:${port}`, `tcp:${port}`);
        if (r.status !== 0) fail(`adb reverse tcp:${port} failed: ${r.stderr.trim()}`);
        console.log(`[build-mobile-gotest] adb reverse tcp:${port} -> this machine:${port}`);
    }
    return true;
}

const cfg = readEnv(join(root, "envs", ".env.gotest.mobile"));
const apiBaseUrl = local || reverseOnly ? (cfg.LOCAL_API_BASE_URL || "http://127.0.0.1:18081") : (cfg.API_BASE_URL ?? "");
if (!/^https?:\/\/\S+$/.test(apiBaseUrl)) fail("API_BASE_URL in envs/.env.gotest.mobile must be an http(s) URL");
if (local || reverseOnly) {
    if (!LOOPBACK.has(new URL(apiBaseUrl).hostname)) {
        fail(`LOCAL_API_BASE_URL must be a loopback URL (${[...LOOPBACK].join(", ")}): ${apiBaseUrl}`);
    }
}
if (reverseOnly) process.exit(reversePorts(apiBaseUrl) ? 0 : 1);
if (local) {
    try {
        const res = await fetch(new URL("/healthz", apiBaseUrl), { signal: AbortSignal.timeout(5000) });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
    } catch (e) {
        console.warn(`[build-mobile-gotest] warning: ${apiBaseUrl}/healthz did not answer (${e.message}); start the ` +
            "stack (logitrack-api: make up) and docker start logitrack-api-fwd-public before opening the app");
    }
} else if (LOOPBACK.has(new URL(apiBaseUrl).hostname)) {
    fail("API_BASE_URL is a loopback URL: use pnpm build:mobile:gotest:local for the local stack");
}
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

// The install-over build uses the dev flavor's google-services.json: it must be the same (non-prod) project as
// envs/.env.dev.mobile, so the dev 3.x app it replaces and the exchange's Firebase ID tokens share one project (R18).
const firebaseApp = upgradeTest ? googleServices("dev") : gotest;
if (upgradeTest) {
    if (!firebaseApp.packages.includes(DEV_PACKAGE)) fail(`src/dev/google-services.json has no client for ${DEV_PACKAGE}`);
    if (firebaseApp.projectId === prodProject) fail(`src/dev/google-services.json is the prod project (${prodProject})`);
    if (firebaseApp.projectId !== devEnv.FIREBASE_PROJECT_ID) {
        fail(`src/dev/google-services.json (${firebaseApp.projectId}) and envs/.env.dev.mobile ` +
            `(${devEnv.FIREBASE_PROJECT_ID}) name different Firebase projects`);
    }
}

// Google sign-in (developer-spec §11.4, §11.13): the server client id, i.e. the OAuth web client of the dev project.
// Go accepts the token only when this id is in its GOOGLE_OIDC_ALLOWED_CLIENT_IDS. Empty = the button is disabled.
const googleClientId = (cfg.GOOGLE_OIDC_CLIENT_ID || devEnv.FIREBASE_WEB_CLIENT_ID || "").trim();
if (googleClientId && !firebaseApp.webClientIds.includes(googleClientId)) {
    fail("GOOGLE_OIDC_CLIENT_ID (or FIREBASE_WEB_CLIENT_ID of envs/.env.dev.mobile) is not an OAuth web client of " +
        `src/${FLAVOR}/google-services.json (${firebaseApp.projectId})`);
}
if (!googleClientId) console.warn("[build-mobile-gotest] warning: no Google client id; Google sign-in is disabled");

// A local build has its own version, so an APK made for this machine is never mistaken for a staging one. The
// install-over build's versionCode sits above any dev build (400000 + N), so `adb install -r` can replace dev 3.x.
const buildName = `4.0.0-gotest.${build}${upgradeTest ? ".upgrade" : local ? ".local" : ""}`;
const buildNumber = upgradeTest ? 400000 + build : build;
const args = [
    "build", "apk", "--release",
    "--flavor", FLAVOR,
    "--dart-define=FLAVOR=gotest",
    `--dart-define=API_BASE_URL=${apiBaseUrl}`,
    `--dart-define=GOOGLE_OIDC_CLIENT_ID=${googleClientId}`,
    `--build-name=${buildName}`,
    `--build-number=${buildNumber}`,
    ...(upgradeTest ? ["--android-project-arg=logitrackUpgradeTest=true"] : []),
];
console.log(`[build-mobile-gotest] ${TARGET_PACKAGE} ${buildName} (${buildNumber}) -> ${apiBaseUrl} (Firebase ${firebaseApp.projectId})`);
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
if (status === 0 && local) {
    const apk = join(mobile, "build", "app", "outputs", "flutter-apk", `logitrack-${FLAVOR}Release-v${buildName}.apk`);
    if (!existsSync(apk)) fail(`built, but ${apk} is missing`);
    if (reversePorts(apiBaseUrl)) {
        const r = adb("install", "-r", apk);
        process.stdout.write(r.stdout);
        if (r.status !== 0) {
            const hint = /UPDATE_INCOMPATIBLE/.test(r.stderr)
                ? " (the installed dev app was signed with another key: build dev 3.x on this machine first)" : "";
            fail(`adb install failed: ${r.stderr.trim()}${hint}`);
        }
        console.log(upgradeTest
            ? "[build-mobile-gotest] installed over the dev app: open 'LogiTrack DEV'; a signed-in 3.x session should land on Home"
            : "[build-mobile-gotest] installed: open 'LogiTrack GO-TEST' and log in with a seeded driver");
    } else {
        console.log(`[build-mobile-gotest] APK: ${apk}`);
    }
}
process.exit(status);
