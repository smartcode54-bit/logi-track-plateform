/** Time labels of the Go chat console (instants from Go are RFC 3339 strings). */

const locale = (language: string) => (language === "th" ? "th-TH" : "en-US");

function parse(iso: string | null | undefined): Date | null {
    if (!iso) return null;
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? null : d;
}

/** A list row's time: the clock today, the day before. */
export function chatTimeLabel(iso: string | null | undefined, language: string, now: Date = new Date()): string {
    const d = parse(iso);
    if (!d) return "";
    if (d.toDateString() === now.toDateString()) return d.toLocaleTimeString(locale(language), { hour: "2-digit", minute: "2-digit" });
    return d.toLocaleDateString(locale(language), { month: "short", day: "numeric" });
}

/** A message's clock time. */
export function clockLabel(iso: string | null | undefined, language: string): string {
    const d = parse(iso);
    return d ? d.toLocaleTimeString(locale(language), { hour: "2-digit", minute: "2-digit" }) : "";
}

/** A date or date-time cell of the broadcast history. */
export function dateTimeLabel(iso: string | null | undefined, language: string): string {
    const d = parse(iso);
    return d ? d.toLocaleString(locale(language), { dateStyle: "short", timeStyle: "short" }) : "—";
}

/** The calendar day (local) of an instant, for the room's day separators. */
export function localDayKey(iso: string): string {
    const d = parse(iso);
    return d ? `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}` : "";
}

/** The day separator: today, yesterday, else the long date. */
export function dayLabel(iso: string, language: string, today: string, yesterday: string, now: Date = new Date()): string {
    const d = parse(iso);
    if (!d) return "";
    const start = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
    const diff = Math.round((start(now) - start(d)) / 86_400_000);
    if (diff === 0) return today;
    if (diff === 1) return yesterday;
    return d.toLocaleDateString(locale(language), { weekday: "long", year: "numeric", month: "long", day: "numeric" });
}
