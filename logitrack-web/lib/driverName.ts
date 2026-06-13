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
