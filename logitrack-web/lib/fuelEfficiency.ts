/**
 * Plausible km/L range for our trucks. A fill-to-fill reading outside it almost always means a
 * missed refuel or a partial fill, so it is not trusted (fuel page average, P&L ฿/km — ADR 0031).
 */
export const KMPL_MIN = 2;
export const KMPL_MAX = 25;

/** Group key for a truck from an expense row: truckId first, else the normalized plate. */
export function normalizePlateKey(plate: string | null | undefined): string {
    return String(plate ?? "").replace(/[\s-]/g, "").toUpperCase();
}
