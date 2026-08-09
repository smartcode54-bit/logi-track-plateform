/**
 * Calendar dates for billing announcements (ADR 0009 §2).
 *
 * A price round is announced by **date** ("มีผล 16 ส.ค."), and that date means Bangkok midnight —
 * not the admin's local midnight, and not UTC midnight. The previous helper built
 * `Date.UTC(y, m, d)` from *local* getters, so a round stored as "16 Aug" actually began at
 * 16 Aug 07:00 ICT and every overnight delivery on a switch day was priced at the previous round.
 *
 * Thailand is a fixed UTC+07:00 with no DST, so a literal offset is exact and, unlike
 * `new Date("...T00:00:00")`, gives the same instant on every machine regardless of its timezone.
 */

/** `yyyy-MM-dd` → the instant of Bangkok midnight that day. */
export function bangkokMidnightFromDateStr(dateStr: string): Date | null {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(dateStr)) return null;
    const d = new Date(`${dateStr}T00:00:00+07:00`);
    return Number.isNaN(d.getTime()) ? null : d;
}

/** `yyyy-MM-dd` of an instant, read on the Bangkok calendar. */
export function bangkokDateStr(value: Date): string {
    const fmt = new Intl.DateTimeFormat("en-CA", {
        timeZone: "Asia/Bangkok",
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
    });
    return fmt.format(value);
}

/**
 * Normalize a `Date` coming from a date picker to Bangkok midnight of the day it represents.
 *
 * The picker hands back local midnight, so the calendar day is read with the **local** getters that
 * produced it — reading it in Bangkok instead would shift the day for any admin west of ICT.
 */
export function bangkokMidnightFromPickedDate(value: Date): Date {
    const y = value.getFullYear();
    const m = String(value.getMonth() + 1).padStart(2, "0");
    const d = String(value.getDate()).padStart(2, "0");
    const parsed = bangkokMidnightFromDateStr(`${y}-${m}-${d}`);
    // The template above is always well-formed, so this fallback is unreachable in practice.
    return parsed ?? value;
}

/** The `yyyy-MM-dd` a picked `Date` denotes, using the same local reading as the helper above. */
export function pickedDateToDateStr(value: Date): string {
    const y = value.getFullYear();
    const m = String(value.getMonth() + 1).padStart(2, "0");
    const d = String(value.getDate()).padStart(2, "0");
    return `${y}-${m}-${d}`;
}
