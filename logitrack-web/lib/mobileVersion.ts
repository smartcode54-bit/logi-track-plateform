/**
 * Driver-app version comparison — the single source of truth for "is this installation behind?".
 *
 * Used by the Mobile Clients table (per-install badge) and by the mobile-release page (the impact
 * count shown before an admin forces the fleet to update). See
 * shared-docs/adr/0007-mobile-forced-update-pipeline.md.
 *
 * Deliberately hand-rolled rather than pulling in `semver`: the inputs are strictly `MAJOR.MINOR.PATCH`
 * straight out of `logitrack-mobile/pubspec.yaml`, and the mobile side already parses them with
 * `pub_semver`. This mirrors that comparison in TypeScript and nothing more.
 *
 * The rule this module exists to enforce: **never compare versions as strings.** `"2.10.0" < "2.9.3"`
 * lexicographically, so a string compare marks the newest build as outdated and — on the mobile side —
 * would let a stale install through the gate. Every comparison goes through `compareSemver`.
 *
 * Not to be confused with `lib/app-version.ts`, which is the *web admin's* own version from
 * package.json. This module is about the Flutter driver app.
 */

/** A parsed `MAJOR.MINOR.PATCH` triple. */
export type Semver = readonly [number, number, number];

/**
 * Where one installation sits relative to the published release and the enforced floor.
 *
 * - `blocked`  — below `minAllowedVersion`; the app refuses to run right now.
 * - `outdated` — runnable, but behind the latest published build.
 * - `ahead`    — newer than what we published. Usually a dev build loose in the fleet.
 * - `current`  — on the latest published build.
 * - `unknown`  — the installed version, or the reference we compare against, could not be parsed.
 */
export type VersionStatus = "current" | "outdated" | "blocked" | "ahead" | "unknown";

/**
 * Parse a `MAJOR.MINOR.PATCH` string, tolerating a trailing `+build` suffix (pubspec writes `2.9.3+1`).
 *
 * Returns null for anything else — including `"v2.9.3"`, `"2.9"` and the `"—"` placeholder the table
 * renders for missing data. Callers must treat null as "unknown", never as zero.
 */
export function parseSemver(raw: unknown): Semver | null {
    if (typeof raw !== "string") return null;

    const withoutBuild = raw.trim().split("+")[0];
    if (!/^\d+\.\d+\.\d+$/.test(withoutBuild)) return null;

    const parts = withoutBuild.split(".").map((p) => Number.parseInt(p, 10));
    if (parts.some((n) => !Number.isSafeInteger(n))) return null;

    return [parts[0], parts[1], parts[2]] as const;
}

/**
 * Compare two version strings numerically: negative if `a < b`, 0 if equal, positive if `a > b`.
 *
 * Returns null when either side is unparseable, so callers are forced to handle "unknown" instead of
 * silently getting a falsy 0 that reads as "equal".
 */
export function compareSemver(a: unknown, b: unknown): number | null {
    const left = parseSemver(a);
    const right = parseSemver(b);
    if (!left || !right) return null;

    for (let i = 0; i < 3; i++) {
        if (left[i] !== right[i]) return left[i] - right[i];
    }
    return 0;
}

/**
 * Classify one installation against the published release and the enforced floor.
 *
 * `blocked` is checked before `outdated` because it is the more urgent fact: an install can be both,
 * and the operator needs to see the one that means "this driver cannot work right now".
 *
 * Either reference may be missing (a project that has never published, or never set a floor); the
 * corresponding comparison is simply skipped rather than treated as version zero.
 */
export function getVersionStatus(
    installed: unknown,
    latest: unknown,
    minAllowed: unknown,
): VersionStatus {
    if (!parseSemver(installed)) return "unknown";

    const vsMin = compareSemver(installed, minAllowed);
    if (vsMin !== null && vsMin < 0) return "blocked";

    const vsLatest = compareSemver(installed, latest);
    if (vsLatest === null) return "unknown";
    if (vsLatest < 0) return "outdated";
    if (vsLatest > 0) return "ahead";
    return "current";
}

/** Per-status totals for the summary line above the Mobile Clients table. */
export interface VersionStatusCounts {
    current: number;
    outdated: number;
    blocked: number;
    ahead: number;
    unknown: number;
}

/** Tally `getVersionStatus` across a set of installed-version strings. */
export function countVersionStatuses(
    installedVersions: readonly unknown[],
    latest: unknown,
    minAllowed: unknown,
): VersionStatusCounts {
    const counts: VersionStatusCounts = {
        current: 0,
        outdated: 0,
        blocked: 0,
        ahead: 0,
        unknown: 0,
    };
    for (const installed of installedVersions) {
        counts[getVersionStatus(installed, latest, minAllowed)] += 1;
    }
    return counts;
}

/**
 * How many of `installedVersions` would be locked out if the floor were raised to `targetVersion`.
 *
 * This is the number shown to the admin before they confirm a forced update, so it counts only what
 * we can prove: an install whose version does not parse is *not* counted as affected — we do not know
 * that it is behind, and inflating the number would make the confirmation dialog lie.
 */
export function countBlockedByFloor(
    installedVersions: readonly unknown[],
    targetVersion: unknown,
): number {
    return installedVersions.reduce<number>((total, installed) => {
        const cmp = compareSemver(installed, targetVersion);
        return cmp !== null && cmp < 0 ? total + 1 : total;
    }, 0);
}
