/**
 * Resolve a driver's display name for reports / billing / exports.
 *
 * Business rule: driver names are shown in **Thai** across all reports, so the
 * Thai full name (`fullNameTh`) takes priority. Falls back to the legacy `name`
 * field, then `firstName + lastName`, then email, then the provided id.
 */
export function driverDisplayName(
    d: Record<string, unknown> | null | undefined,
    fallbackId?: string
): string {
    if (!d) return fallbackId ?? "";
    const fullNameTh = String((d.fullNameTh as string) ?? "").trim();
    if (fullNameTh) return fullNameTh;
    const name = String((d.name as string) ?? "").trim();
    if (name) return name;
    const combined = [d.firstName, d.lastName].filter(Boolean).join(" ").trim();
    if (combined) return combined;
    const email = String((d.email as string) ?? "").trim();
    if (email) return email;
    return fallbackId ?? "";
}

/** Minimal shape needed to identify a driver in a picker. */
export type DriverNameSource = {
    id?: string;
    fullNameTh?: string;
    name?: string;
    firstName?: string;
    lastName?: string;
    email?: string;
};

/**
 * Which driver a task form's driver `Select` should show as selected.
 *
 * Prefers the stored `driverId`, because a name is not an identity: two drivers can share one, and a
 * renamed driver would silently deselect. The name match is only a fallback for legacy tasks saved
 * before `driverId` was stored — and it accepts BOTH the Thai display name and the older
 * `firstName lastName` form, since `tasks.driverName` has been written both ways over time.
 *
 * Returns `undefined` when nothing matches, so the caller decides the placeholder value.
 */
export function matchDriverOptionId(
    drivers: DriverNameSource[],
    driverId: string | null | undefined,
    driverName: string | null | undefined
): string | undefined {
    const id = (driverId ?? "").trim();
    if (id && drivers.some((d) => d.id === id)) return id;

    const name = (driverName ?? "").trim();
    if (!name) return undefined;

    const match = drivers.find((d) => {
        if (driverDisplayName(d, "") === name) return true;
        const latin = [d.firstName, d.lastName].filter(Boolean).join(" ").trim();
        return latin !== "" && latin === name;
    });
    return match?.id;
}
