/**
 * Calendar dates (`yyyy-MM-dd`) between the forms and the API (T25). Go takes and returns date-only
 * strings on the Bangkok calendar (Appendix B §B.1.5); the forms hold Dates or strings. No date
 * library: these helpers sit on list pages, whose initial JS has a budget (developer-spec.md §10.11).
 */

const pad = (n: number) => String(n).padStart(2, "0");

/**
 * A date as the API takes it (`yyyy-MM-dd`), from a form or stored value: a date-only string as it
 * is; a Date, an ISO timestamp or a Firestore Timestamp on the local calendar, so a stored midnight
 * never lands on the previous UTC day.
 */
export function apiDate(v: unknown): string | null {
    if (v === undefined || v === null || v === "") return null;
    if (typeof v === "string" && /^\d{4}-\d{2}-\d{2}$/.test(v)) return v;
    const ts = v as { toDate?: () => Date };
    const d = v instanceof Date ? v : typeof ts.toDate === "function" ? ts.toDate() : new Date(String(v));
    if (Number.isNaN(d.getTime())) return null;
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** A calendar date (`yyyy-MM-dd`) as a local Date (a DatePicker value); other strings as parsed. */
export function localDate(v: string | null | undefined): Date | undefined {
    if (!v) return undefined;
    const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(v);
    if (m) return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
    const d = new Date(v);
    return Number.isNaN(d.getTime()) ? undefined : d;
}
