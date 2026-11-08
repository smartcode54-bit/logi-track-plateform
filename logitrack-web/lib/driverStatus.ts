/**
 * Driver pickers in create forms list only drivers who still work here. `drivers.status` is
 * "Active" | "Inactive" | "On-Duty" (validate/driverSchema.ts); "Available" is a legacy value the
 * truck-assignment picker already accepts. A doc with no status counts as active — the schema
 * default — so drivers saved before the field existed don't vanish. Anything else (Inactive and
 * legacy values such as Terminated) is left out.
 *
 * Filter lists, history and reports keep every driver: past records still belong to them.
 */
const ACTIVE_DRIVER_STATUSES = new Set(["active", "on-duty", "available"]);

export function isDriverActive(driver: { status?: unknown } | null | undefined): boolean {
    const status = String(driver?.status ?? "").trim().toLowerCase();
    return status === "" || ACTIVE_DRIVER_STATUSES.has(status);
}

/**
 * Active drivers, plus any driver the record already holds (matched by doc id or Auth UID) so an
 * edit never blanks out a driver or helper who has since been made inactive.
 */
export function selectableDrivers<T extends { id?: string; authId?: string; status?: unknown }>(
    drivers: T[],
    keepIds: Array<string | null | undefined> = []
): T[] {
    const keep = new Set(keepIds.map((id) => (id ?? "").trim()).filter(Boolean));
    return drivers.filter(
        (d) => isDriverActive(d) || (!!d.id && keep.has(d.id)) || (!!d.authId && keep.has(d.authId))
    );
}
