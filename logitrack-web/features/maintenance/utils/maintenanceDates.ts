/** Date-only and appointment values use the operation's Bangkok calendar, independent of the browser. */
export function bangkokWallTime(instant: string | Date): string {
    const date = new Date(instant);
    if (Number.isNaN(date.getTime())) return "";
    const parts = new Intl.DateTimeFormat("en-GB", {
        timeZone: "Asia/Bangkok", year: "numeric", month: "2-digit", day: "2-digit",
        hour: "2-digit", minute: "2-digit", hourCycle: "h23",
    }).formatToParts(date);
    const part = (type: Intl.DateTimeFormatPartTypes) => parts.find((p) => p.type === type)?.value ?? "";
    return `${part("year")}-${part("month")}-${part("day")}T${part("hour")}:${part("minute")}`;
}

export function maintenanceDay(value: string | Date): string {
    // Date inputs and unzoned timestamps already name a Bangkok wall day.
    if (typeof value === "string" && /^\d{4}-\d{2}-\d{2}(?:$|T[\d:.]+$)/.test(value)) return value.slice(0, 10);
    return bangkokWallTime(value).slice(0, 10);
}
