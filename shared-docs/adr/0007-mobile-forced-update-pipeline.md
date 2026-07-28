# ADR 0007 — Mobile forced-update pipeline (announce with the build, enforce with a button)

- **Status:** Accepted (2026-07-28) — implemented locally, pending dev verification and deploy
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** root `package.json` (build), `logitrack-web` (release script, Security Center, Storage rules),
  `logitrack-mobile` (`core/services/mobile_app_version_service.dart`)

## Context

Getting a new driver APK into the fleet was entirely manual: build locally, upload the file somewhere
by hand, then open the Firebase Console and edit `settings/mobile_app.minAllowedVersion` and
`apkDownloadUrl`. **No code in the repo wrote that document** — a grep for either field found exactly
one reader (`mobile_app_version_service.dart`) and no writer. The Mobile Clients page
(`/app/security-center/mobile-clients`) rendered `appVersion` as a raw string with nothing to compare
it against, so "who is still on an old build?" had no answer.

Three findings shaped the design.

### 1. The prod build script did not produce a prod build

Root `package.json:13` ran `flutter build apk --split-per-abi` with **neither `--flavor prod` nor
`--dart-define=FLAVOR=prod`**. The two flags do different jobs and neither derives the other:

- `--flavor prod` selects the Gradle product flavor → `applicationId com.wrt.logitrack`, prod
  `google-services.json` (`android/app/build.gradle.kts:45-57`).
- `--dart-define=FLAVOR=prod` sets the Dart compile-time constant read at `main.dart:44` (which dotenv
  file to load), `login_page.dart:13` (the Release/Dev footer label) and
  `mobile_client_heartbeat_service.dart:62` (the `flavor` reported to Firestore).

So the "prod" APK loaded `.env.dev` and reported `flavor: "dev"` in its heartbeat. Any version
announcement built on that would be comparing versions across two different environments. The correct
command was already written down in `android/key.properties.example:11`; it had simply never been
copied into the npm script.

`--split-per-abi` was removed at the same time. It currently produces nothing —
`build/app/outputs/apk/prod/release/output-metadata.json` reports a single `"type": "SINGLE"` element
with empty `filters` — and if it ever engaged, `build.gradle.kts:88-90` assigns the *same*
`outputFileName` to every element while `:103` copies only `outputs.first()`, so all three ABI APKs
would collide on one filename and the build would silently ship one architecture's APK under a
universal-sounding name.

### 2. The force gate was defeated by airplane mode

`mobile_app_version_service.dart` wrapped everything in `catch (_) => true`. That blanket fail-open is
**correct** for a fleet of truck drivers in patchy signal — a Firestore read that times out must never
stop someone mid-delivery. But it also meant a driver who was *already blocked* could enable airplane
mode, force-stop the app and reopen: the read fails, the catch returns true, and the forced update is
defeated by turning off mobile data.

### 3. "Force" is not reversible for anyone who has already been stopped

Raising the floor blocks every installation below it at next launch or resume. Lowering it afterwards
un-blocks drivers who have not updated, but does not undo an install that already happened. This is a
fleet-wide kill switch, and it needed to be treated as one.

## Decision

### 1. The build announces; only a human enforces

Two writers of `settings/mobile_app` with **deliberately disjoint field sets**:

| Writer | Fields |
|---|---|
| `logitrack-web/scripts/publish-mobile-release.mjs` | `latestVersion`, `latestBuildNumber`, `apkDownloadUrl`, `apkSizeBytes`, `apkSha256`, `flavor`, `releasedAt`, `releasedBy`, `releaseNotes` |
| `/app/security-center/mobile-release` | `minAllowedVersion`, `minAllowedVersionSetAt`, `minAllowedVersionSetBy` |

`minAllowedVersion` is the only field the mobile gate reads to decide whether to block. **The release
script must never write it** — there is a literal assertion in the script to that effect. Publishing a
build therefore cannot lock anyone out; a person raises the floor, deliberately, once they are ready.

The alternative — block anything that is not exactly `latestVersion` — was rejected: it stops drivers
mid-shift the instant a build is published, with no way to stage the rollout.

Firestore rules are unchanged (`firestore.rules:219-222`: authenticated read, `isWebAdmin()` write).
**Accepted limitation:** rules cannot enforce the writer/field split, and `settings/*` is readable by
every authenticated user including drivers. The split is a property of the code, not of the database.

### 2. The APK lives in Firebase Storage behind a download token

Object path `app_releases/<flavor>/logitrack-<flavor>-v<version>.apk`, immutable: publishing over an
existing version requires `--force`, which issues a new download token and therefore **breaks any URL
already announced**.

The published URL is a Firebase download-token URL
(`?alt=media&token=<uuid>`, set via the `firebaseStorageDownloadTokens` object metadata). The two
alternatives do not work here: the buckets are the newer `.firebasestorage.app` kind with uniform
bucket-level access, so per-object `makePublic()` returns 400; and v4 signed URLs expire after 7 days,
far too short for a link drivers are told to install from.

`storage.rules` gets `allow read: if true; allow write: if false` for `app_releases/**`. The `write`
is closed unlike every other block in that file — the only writer is the Admin SDK, which bypasses
rules entirely, so closing it costs nothing and stops any authenticated driver from replacing the APK
the whole fleet is being told to install.

### 3. The script refuses to guess

`--project=dev|prod` is required with no default, and the script hard-fails when the service account
belongs to a different project. Before writing anything it cross-checks the built artifact against
`output-metadata.json`: `versionName` must equal the pubspec version (catches "edited pubspec, forgot
to rebuild"), and `applicationId` must match the flavor — **which is what makes decision 1's flavor fix
self-enforcing**, since a prod build made without `--flavor prod` is rejected at publish time. After
upload it HEADs the published URL and checks the status and content-length before touching Firestore,
so an upload whose URL does not serve never becomes a release.

### 4. The force button cannot be pressed without a download link

`mobile_app_version_service.dart` renders the download action only when `apkDownloadUrl` is non-empty.
Forcing an update without one leaves every driver on an undismissable dialog with no button and no way
out. The button is therefore disabled whenever the URL is blank — the single most important guard in
this feature — and also when there is no published version, or when the floor already covers it.

Confirmation requires **typing the target version string**, not a yes/no, and shows a live count of how
many installations seen in the last 7 days would be blocked. That count is the real consent signal;
unparseable versions are excluded from it, because a dialog that overstates its impact teaches people
to ignore it.

No `security_events` audit row this round — that collection is written only by Cloud Functions, and a
callable is new infrastructure. `minAllowedVersionSetAt` / `SetBy` on the doc are the audit trail.

### 5. A new admin-only capability, not a reuse

`security_manage_mobile_release`. Reusing `security_view_mobile_clients` was rejected: it is granted to
`partner` by default (`lib/roles.ts:128`). The `/app/security-center` subtree is already admin-gated by
`lib/permissions.ts:55-57`, but relying on that coincidence to protect a fleet-wide kill switch is not
a design.

### 6. The gate caches the last floor it saw, and still fails open without one

Every successful read persists `minAllowedVersion` + `apkDownloadUrl` + a timestamp to
SharedPreferences; a failed read falls back to that cache. This closes the airplane-mode hole while
keeping the offline behaviour that matters:

- The cache can only hold a floor **this device actually saw**, so a driver who has never been online
  since the floor was raised is never blocked by it.
- Being blocked offline requires having already been blocked online — the driver was already stopped
  and told to update. Nothing new breaks.
- A transient App Check or permission-denied error cannot newly lock anyone out, because the cached
  value is one the device already satisfied or already failed.
- Past a 30-day TTL the cache is ignored. A device offline for a month gets sorted out at the depot,
  not bricked in a truck.
- No cache at all (fresh install, never online) → fail open, exactly as before.

A successful read that finds **no** floor clears the cache, so removing or lowering
`minAllowedVersion` genuinely un-blocks devices rather than leaving them pinned to a stale value.

Parse failures (`Version.parse` on either side) stay fail-open: those are client-integrity problems,
not evasion vectors.

Explicitly **not** done: no Firestore listener on the config. A live listener would let the floor slam
down mid-delivery, which contradicts decision 1. The check runs at login and on resume, as before.

## Consequences

- Publishing is one command per environment and produces an immutable, hashed, verified artifact.
- Blocking the fleet takes a deliberate second step by an admin, with the blast radius shown first.
- **The gate hardening ships *in* the next APK**, so it protects the *next* forced update, not the
  first one. It must land in the same build that becomes the first script-published release.
- Version comparison now exists twice — `pub_semver` on mobile, `lib/mobileVersion.ts` on web. The TS
  side is hand-rolled (no `semver` dependency) and covered by `lib/mobileVersion.test.ts`; the test
  that matters is `2.10.0 > 2.9.3`, the lexicographic trap that would otherwise mark the newest build
  as outdated.
- `versionCode` remains permanently `1` because pubspec has always used `+1`. Install-over works, so
  distribution is unaffected, but `buildNumber` carries no ordering information — consistent with the
  gate never having compared it. Left as a follow-up.
- Still manual: no mobile CI, so APKs are built on a developer machine and published from there.

## Verification

Dev-only rehearsal, in order: storage rules deployed and a token URL curled; `pnpm --filter logi-track
test`; `build:mobile:dev` and `build:mobile:prod` checked against `output-metadata.json` for
`applicationId` and `versionName`; the prod APK installed and its login footer confirmed to read
"Release" (proof the dart-define landed) with `mobile_installations.flavor == "prod"`; each script
guard tripped deliberately; `release:mobile:dev:apply` confirmed to leave `minAllowedVersion`
untouched; the force button confirmed disabled with an empty URL; one device driven through
block → download → install → unblock; the airplane-mode evasion reproduced before the cache and
confirmed closed after it; and the floor lowered again to prove the lever is two-way.
