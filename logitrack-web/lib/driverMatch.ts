import { isDriverActive } from "@/lib/driverStatus";

/**
 * Match a driver typed into an import sheet (phone and/or name) to a `drivers` doc, so the task
 * gets a real `driverId`. The mobile app lists a driver's tasks by `tasks.driverId` only, so a
 * task that only carries the typed `driverName` / `driverPhone` never reaches the driver.
 *
 * Phone goes first: it is unique per person and survives spacing/spelling differences in names.
 * Name is the fallback, compared against `fullNameTh`, `name` and `firstName lastName`. Only active
 * drivers are considered, same as the assign form. A match must be unique — two candidates is an
 * error, never a guess.
 */
export interface MatchableDriver {
    id: string;
    mobile?: unknown;
    phone?: unknown;
    fullNameTh?: unknown;
    name?: unknown;
    firstName?: unknown;
    lastName?: unknown;
    status?: unknown;
}

export type DriverMatch<T> =
    | { kind: "none" }                        // sheet gave no phone or name → task stays unassigned
    | { kind: "matched"; driver: T; by: "phone" | "name" }
    | { kind: "notFound" }
    | { kind: "ambiguous" };

/**
 * Thai phone → bare local digits ("0814696727"). Accepts spaces, dashes, "+66"/"66" prefixes, and a
 * number cell where Excel dropped the leading zero (814696727).
 */
export function normalizePhone(v: unknown): string {
    let d = String(v ?? "").replace(/\D/g, "");
    if (d.startsWith("66") && d.length === 11) d = "0" + d.slice(2);
    if (d.length === 9 && !d.startsWith("0")) d = "0" + d;
    return d;
}

const normalizeName = (v: unknown) => String(v ?? "").trim().replace(/\s+/g, " ").toLowerCase();

function driverNames(d: MatchableDriver): string[] {
    const latin = [d.firstName, d.lastName].filter((x) => x != null && String(x).trim() !== "").join(" ");
    return [d.fullNameTh, d.name, latin].map(normalizeName).filter(Boolean);
}

export function matchImportDriver<T extends MatchableDriver>(
    drivers: readonly T[],
    phoneCell: unknown,
    nameCell: unknown
): DriverMatch<T> {
    const phone = normalizePhone(phoneCell);
    const name = normalizeName(nameCell);
    if (!phone && !name) return { kind: "none" };

    const active = drivers.filter((d) => isDriverActive(d));

    if (phone) {
        const byPhone = active.filter((d) => {
            const p = normalizePhone(d.mobile ?? d.phone);
            return p !== "" && p === phone;
        });
        if (byPhone.length === 1) return { kind: "matched", driver: byPhone[0], by: "phone" };
        if (byPhone.length > 1) {
            // Shared number (e.g. a company phone): the name decides, else refuse.
            const narrowed = name ? byPhone.filter((d) => driverNames(d).includes(name)) : [];
            return narrowed.length === 1 ? { kind: "matched", driver: narrowed[0], by: "phone" } : { kind: "ambiguous" };
        }
    }

    if (name) {
        const byName = active.filter((d) => driverNames(d).includes(name));
        if (byName.length === 1) return { kind: "matched", driver: byName[0], by: "name" };
        if (byName.length > 1) return { kind: "ambiguous" };
    }
    return { kind: "notFound" };
}
