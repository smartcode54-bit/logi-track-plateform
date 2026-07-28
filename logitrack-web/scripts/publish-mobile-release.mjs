/**
 * Publish a built driver-app APK: upload it to Firebase Storage and announce the version in
 * Firestore `settings/mobile_app`. See shared-docs/adr/0007-mobile-forced-update-pipeline.md.
 *
 * This script ANNOUNCES a release. It never enforces one. It writes `latestVersion` and everything
 * about the built artifact, and it must never write `minAllowedVersion` — that is the floor that
 * actually blocks the app, and an admin raises it deliberately from
 * /app/security-center/mobile-release once they are ready to lock the fleet out of older builds.
 * There is an assertion below that keeps it that way.
 *
 * Build first (the APK must already exist):
 *   pnpm build:mobile:dev     # or build:mobile:prod
 *
 * Dry-run — needs a service account for the target project (a normal user login is not enough for
 * the Admin SDK):
 *   (PowerShell)
 *   $env:GOOGLE_APPLICATION_CREDENTIALS="D:\real\path\dev-sa.json"
 *   node scripts/publish-mobile-release.mjs --project=dev --dry-run
 *
 * Apply (after the dry-run looks right):
 *   node scripts/publish-mobile-release.mjs --project=dev
 *
 * Options:
 *   --project=dev|prod   REQUIRED. No default — publishing to the wrong project is not recoverable
 *                        by editing a field, because drivers may already have the link.
 *   --dry-run            resolve and hash everything, print the payload, upload nothing, write nothing
 *   --apk=<path>         override APK selection (skips output-metadata.json discovery)
 *   --notes="..."        release notes shown on the admin page
 *   --force              overwrite an already-published object for this version. This regenerates the
 *                        download token, so any previously announced URL STOPS WORKING. Only safe
 *                        while the release has not been announced to drivers.
 *   --verbose            log resolution details
 */

import { initializeApp, cert, applicationDefault } from "firebase-admin/app";
import { getFirestore, FieldValue } from "firebase-admin/firestore";
import { getStorage } from "firebase-admin/storage";
import { createHash, randomUUID } from "crypto";
import { createReadStream, existsSync, readFileSync, statSync } from "fs";
import path from "path";
import { fileURLToPath } from "url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const MOBILE_ROOT = path.resolve(HERE, "../../logitrack-mobile");

const PROJECT_IDS = { dev: "logi-track-wrt-dev", prod: "logitrack-prod" };
/** The prod Gradle flavor sets this applicationId; dev keeps the Flutter default. */
const PROD_APPLICATION_ID = "com.wrt.logitrack";

const dryRun = process.argv.includes("--dry-run");
const verbose = process.argv.includes("--verbose");
const force = process.argv.includes("--force");

function argValue(name) {
    const hit = process.argv.find((a) => a.startsWith(`--${name}=`));
    return hit ? hit.slice(name.length + 3) : null;
}

function fail(message) {
    console.error(`\n✖ ${message}\n`);
    process.exit(1);
}

function log(message) {
    if (verbose) console.log(`  · ${message}`);
}

/** `--project=dev|prod` → the flavor and Firebase project it maps to. */
function resolveTarget() {
    const flavor = argValue("project");
    if (!flavor) {
        fail("Missing --project=dev|prod. This script refuses to guess which project to publish to.");
    }
    if (!PROJECT_IDS[flavor]) {
        fail(`Unknown --project=${flavor}. Expected "dev" or "prod".`);
    }
    return { flavor, projectId: PROJECT_IDS[flavor] };
}

/**
 * Read the version out of pubspec.yaml. No YAML dependency — the line is a fixed shape and a parse
 * failure must stop the release rather than publish a guessed version number.
 */
function readPubspecVersion() {
    const pubspecPath = path.join(MOBILE_ROOT, "pubspec.yaml");
    if (!existsSync(pubspecPath)) fail(`pubspec.yaml not found at ${pubspecPath}`);

    const match = readFileSync(pubspecPath, "utf8").match(/^version:\s*(\d+\.\d+\.\d+)(?:\+(\S+))?\s*$/m);
    if (!match) {
        fail(`Could not parse a MAJOR.MINOR.PATCH version out of ${pubspecPath}. Refusing to guess.`);
    }
    return { versionName: match[1], buildNumber: match[2] ?? "" };
}

/**
 * Locate the built APK via the Gradle build metadata.
 *
 * `output-metadata.json` is authoritative: it carries applicationId, variant and versionName, so it
 * can prove the artifact matches the flavor and version we are about to announce. The copies under
 * build/app/outputs/flutter-apk/ are overwritten across versions and carry no metadata at all.
 */
function locateApk(flavor, expectedVersion) {
    const override = argValue("apk");
    if (override) {
        if (!existsSync(override)) fail(`--apk path does not exist: ${override}`);
        console.log(`APK (from --apk, metadata checks skipped): ${override}`);
        return { apkPath: override, applicationId: null, versionNameInApk: null };
    }

    const outDir = path.join(MOBILE_ROOT, "build/app/outputs/apk", flavor, "release");
    const metaPath = path.join(outDir, "output-metadata.json");
    if (!existsSync(metaPath)) {
        fail(
            `No build metadata at ${metaPath}\n` +
            `  Build the APK first:  pnpm build:mobile:${flavor}`
        );
    }

    const meta = JSON.parse(readFileSync(metaPath, "utf8"));
    const elements = Array.isArray(meta.elements) ? meta.elements : [];
    if (elements.length === 0) fail(`No APK elements listed in ${metaPath}`);

    const universal = elements.find((e) => Array.isArray(e.filters) && e.filters.length === 0);
    if (!universal) {
        fail(
            `This build is split per ABI (${elements.length} artifacts, none universal).\n` +
            `  Publishing one ABI's APK as "the" download bricks installs on every other device.\n` +
            `  Rebuild without --split-per-abi, or pass --apk=<path> if you know what you are doing.`
        );
    }

    // Catches "edited pubspec, forgot to rebuild" — the announced version would not match the bytes.
    if (universal.versionName && universal.versionName !== expectedVersion) {
        fail(
            `Built APK is version ${universal.versionName} but pubspec.yaml says ${expectedVersion}.\n` +
            `  Rebuild before publishing:  pnpm build:mobile:${flavor}`
        );
    }

    // Catches the flavor bug: a "prod" build made without --flavor prod carries the dev applicationId.
    if (flavor === "prod" && meta.applicationId !== PROD_APPLICATION_ID) {
        fail(
            `Built APK has applicationId "${meta.applicationId}", expected "${PROD_APPLICATION_ID}".\n` +
            `  This build was not made with --flavor prod. Rebuild:  pnpm build:mobile:prod`
        );
    }
    if (flavor === "dev" && meta.applicationId === PROD_APPLICATION_ID) {
        fail(`Built APK carries the PROD applicationId but --project=dev was requested. Rebuild for dev.`);
    }

    const apkPath = path.join(outDir, universal.outputFile);
    if (!existsSync(apkPath)) fail(`Metadata points at ${universal.outputFile} but the file is missing in ${outDir}`);

    log(`variant ${meta.variantName}, applicationId ${meta.applicationId}`);
    return { apkPath, applicationId: meta.applicationId, versionNameInApk: universal.versionName ?? null };
}

/** Warn (not fail) when the APK predates the pubspec — a likely forgotten rebuild. */
function warnIfStale(apkPath) {
    const pubspecPath = path.join(MOBILE_ROOT, "pubspec.yaml");
    if (!existsSync(pubspecPath)) return;
    if (statSync(apkPath).mtimeMs < statSync(pubspecPath).mtimeMs) {
        console.warn("⚠  APK is older than pubspec.yaml — it may not include your latest changes.");
    }
}

/** Stream the file through SHA-256; the APK is ~80MB and must not be buffered. */
function sha256File(filePath) {
    return new Promise((resolve, reject) => {
        const hash = createHash("sha256");
        createReadStream(filePath)
            .on("error", reject)
            .on("data", (chunk) => hash.update(chunk))
            .on("end", () => resolve(hash.digest("hex")));
    });
}

function initAdmin(projectId) {
    const keyPath = (process.env.GOOGLE_APPLICATION_CREDENTIALS ?? "").trim().replace(/^["']|["']$/g, "");
    if (keyPath) {
        if (keyPath === "..." || !existsSync(keyPath)) {
            fail(`GOOGLE_APPLICATION_CREDENTIALS does not point to a real SA json file: ${keyPath}`);
        }
        const serviceAccount = JSON.parse(readFileSync(keyPath, "utf8"));
        // Publishing a dev build to prod (or vice versa) hands drivers the wrong APK, so a mismatch
        // between the credential and --project is a hard stop rather than a warning.
        if (serviceAccount.project_id && serviceAccount.project_id !== projectId) {
            fail(
                `Service account belongs to "${serviceAccount.project_id}" but --project resolves to "${projectId}".\n` +
                `  Point GOOGLE_APPLICATION_CREDENTIALS at the ${projectId} service account.`
            );
        }
        initializeApp({ credential: cert(serviceAccount), projectId });
    } else {
        initializeApp({ credential: applicationDefault(), projectId });
    }
}

/**
 * Build the durable download URL.
 *
 * Firebase download-token URLs are the only durable public option here: the buckets are the new
 * `.firebasestorage.app` kind with uniform bucket-level access, so per-object `makePublic()` fails,
 * and v4 signed URLs expire after 7 days — far too short for a link drivers are told to install from.
 */
function downloadUrl(bucketName, objectPath, token) {
    return `https://firebasestorage.googleapis.com/v0/b/${bucketName}/o/${encodeURIComponent(objectPath)}?alt=media&token=${token}`;
}

async function main() {
    const { flavor, projectId } = resolveTarget();
    const { versionName, buildNumber } = readPubspecVersion();
    const { apkPath } = locateApk(flavor, versionName);
    warnIfStale(apkPath);

    const bucketName =
        argValue("bucket") || process.env.FIREBASE_STORAGE_BUCKET || `${projectId}.firebasestorage.app`;
    const objectPath = `app_releases/${flavor}/logitrack-${flavor}-v${versionName}.apk`;
    const sizeBytes = statSync(apkPath).size;

    console.log(`Firestore project: ${projectId}${dryRun ? " (dry-run — no writes)" : " (APPLY — will write)"}`);
    console.log(`Storage bucket:    ${bucketName}`);
    console.log(`APK:               ${apkPath}`);
    console.log(`Version:           ${versionName}${buildNumber ? `+${buildNumber}` : ""} (${flavor})`);
    console.log(`Size:              ${(sizeBytes / 1024 / 1024).toFixed(1)} MB`);

    process.stdout.write("Hashing… ");
    const sha256 = await sha256File(apkPath);
    console.log(sha256.slice(0, 16) + "…");

    initAdmin(projectId);
    const bucket = getStorage().bucket(bucketName);
    const file = bucket.file(objectPath);

    const [exists] = await file.exists();
    if (exists && !force) {
        const collision =
            `${objectPath} already exists.\n` +
            `  A published version is immutable so "which build is ${versionName}" stays answerable.\n` +
            `  Bump the version in pubspec.yaml and rebuild, or pass --force to overwrite.\n` +
            `  NOTE: --force issues a new download token, breaking any URL already announced.`;
        // In dry-run this is the finding, not a crash — the operator asked what would happen.
        if (dryRun) console.warn(`\n⚠  ${collision}`);
        else fail(collision);
    }

    const token = randomUUID();
    const url = downloadUrl(bucketName, objectPath, token);

    const payload = {
        latestVersion: versionName,
        latestBuildNumber: buildNumber,
        apkDownloadUrl: url,
        apkSizeBytes: sizeBytes,
        apkSha256: sha256,
        flavor,
        releasedAt: FieldValue.serverTimestamp(),
        releasedBy: `script:${process.env.USERNAME || process.env.USER || "unknown"}`,
        ...(argValue("notes") ? { releaseNotes: argValue("notes") } : {}),
    };

    // The whole safety property of this feature: publishing announces, it never enforces.
    if ("minAllowedVersion" in payload) {
        fail("Internal error: the release script must never write minAllowedVersion.");
    }

    if (dryRun) {
        console.log(`\nWould upload to:   gs://${bucketName}/${objectPath}`);
        console.log(`Would publish URL: ${url.replace(token, "<token>")}`);
        console.log(`Would merge into   settings/mobile_app:`);
        console.log(JSON.stringify({ ...payload, releasedAt: "<serverTimestamp>" }, null, 2));
        console.log("\nDry run — nothing uploaded, nothing written.");
        return;
    }

    process.stdout.write("Uploading… ");
    await bucket.upload(apkPath, {
        destination: objectPath,
        resumable: true,
        metadata: {
            contentType: "application/vnd.android.package-archive",
            cacheControl: "public, max-age=31536000, immutable",
            metadata: {
                firebaseStorageDownloadTokens: token,
                appVersion: versionName,
                buildNumber,
                flavor,
                sha256,
            },
        },
    });
    console.log("done");

    // Verify the published URL actually serves before recording it. An upload that succeeded but
    // whose URL 404s must not become a release drivers are told to install from.
    process.stdout.write("Verifying URL… ");
    const head = await fetch(url, { method: "HEAD" });
    if (!head.ok) {
        fail(`Uploaded, but the download URL returned ${head.status}. Firestore was NOT updated.`);
    }
    const served = Number.parseInt(head.headers.get("content-length") ?? "0", 10);
    if (served && served !== sizeBytes) {
        fail(`Uploaded, but the URL serves ${served} bytes instead of ${sizeBytes}. Firestore was NOT updated.`);
    }
    console.log(`${head.status} OK`);

    // merge:true so an admin-set minAllowedVersion on this doc survives untouched.
    await getFirestore().collection("settings").doc("mobile_app").set(payload, { merge: true });

    console.log(`\n✔ Published ${versionName} (${flavor}) to ${projectId}`);
    console.log(`  ${url}`);
    console.log(`\n  Drivers are NOT blocked yet. To enforce it, open`);
    console.log(`  /app/security-center/mobile-release and press "Force all drivers to ${versionName}".`);
}

main().catch((e) => {
    console.error(e);
    process.exit(1);
});
