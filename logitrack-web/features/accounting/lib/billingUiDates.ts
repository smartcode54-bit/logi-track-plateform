import { bangkokDateStr } from "@/lib/billingDate";

/** A delivered instant as a Bangkok datetime-local value, also used by Excel. */
export function bangkokDateTimeInput(value: string | null | undefined): string {
    if (!value) return "";
    const date = new Date(value);
    if (!Number.isFinite(date.getTime())) return "";
    const time = new Intl.DateTimeFormat("en-GB", { timeZone: "Asia/Bangkok", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).format(date);
    return `${bangkokDateStr(date)}T${time}`;
}

export function bangkokInputInstant(value: string): string {
    return new Date(`${value}:00+07:00`).toISOString();
}

export function bangkokExcelDateTime(value: string | null | undefined): string {
    return bangkokDateTimeInput(value).replace("T", " ");
}

export function formatBangkokDateTime(value: string | null | undefined): string {
    const local = bangkokDateTimeInput(value);
    if (!local) return "—";
    const [day, time] = local.split("T");
    return `${formatBillingDay(day)} ${time}`;
}

/** billingAxisDate is already a Bangkok calendar day. */
export function formatBillingDay(value: string | null | undefined): string {
    if (!value) return "—";
    const [year, month, day] = value.slice(0, 10).split("-");
    return year && month && day ? `${day}/${month}/${year}` : "—";
}

export function bangkokMonthRange(now = new Date()) {
    const day = bangkokDateStr(now);
    const [year, month] = day.split("-").map(Number);
    return { from: `${day.slice(0, 7)}-01`, to: `${day.slice(0, 7)}-${new Date(Date.UTC(year, month, 0)).getUTCDate()}`, year, month };
}

export function bangkokLast30Days(now = new Date()) {
    return { from: bangkokDateStr(new Date(now.getTime() - 29 * 86400000)), to: bangkokDateStr(now) };
}
