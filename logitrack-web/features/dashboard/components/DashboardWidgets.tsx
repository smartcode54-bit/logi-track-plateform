"use client";

/**
 * The side widgets of the dashboard over Go (T54R; Appendix E §E.2.1, §E.5 rows 21, 22): the
 * current-position list (`['vehicleLocations']`, shared with the map), the pending-expense and chat
 * counters (`['badges']`, the sidebar's key: no listener on `vehicle_expenses` or `chats`), the
 * recent-updates feed and the compliance alerts (the summary's `recent` and `compliance` blocks). Each
 * renders only from data its principal may read; none fetches by itself.
 */
import Link from "next/link";
import { AlertTriangle, ArrowRight, CalendarDays, CheckCircle, CheckCircle2, ClipboardCheck, ExternalLink, Loader2, MapPin, MessageCircle, RefreshCw, ShieldAlert, Truck, Wrench, type LucideIcon } from "lucide-react";

import { useLanguage } from "@/context/language";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import type { ComplianceBlockDTO, ComplianceItemDTO, RecentItemDTO } from "../api/summary";
import type { VehicleLocationDTO } from "../api/vehicleLocationEvents";

type Translate = (key: string, fallbackOrParams?: string | Record<string, string | number>) => string;

/** "just now", "5 minutes ago", ... from the en/th catalogue; a date after a week. */
export function timeAgo(at: string, t: Translate, now: number = Date.now()): string {
    const d = new Date(at);
    const sec = Math.floor((now - d.getTime()) / 1000);
    if (sec < 60) return t("dashboard.updates.time.justNow");
    if (sec < 3600) return t("dashboard.updates.time.minutesAgo", { minutes: Math.floor(sec / 60) });
    if (sec < 86400) return t("dashboard.updates.time.hoursAgo", { hours: Math.floor(sec / 3600) });
    if (sec < 604800) return t("dashboard.updates.time.daysAgo", { days: Math.floor(sec / 86400) });
    return d.toLocaleDateString();
}

// --- current positions -----------------------------------------------------------------------------

export function VehiclePositionList({ vehicles, loading, error, onRetry }: { vehicles: readonly VehicleLocationDTO[]; loading: boolean; error: boolean; onRetry: () => void }) {
    const { t } = useLanguage();
    return (
        <Card className="bg-card border border-border" data-testid="vehicle-positions">
            <CardHeader className="pb-2">
                <div className="flex items-center justify-between">
                    <h3 className="text-sm font-semibold text-foreground flex items-center gap-2">
                        <Truck className="h-4 w-4 text-muted-foreground" />
                        {t("dashboard.vehiclePosition.title")}
                    </h3>
                    <Link href="/app/driver-monitor" className="text-xs font-medium text-primary hover:underline">
                        {t("dashboard.vehiclePosition.viewAll")}
                    </Link>
                </div>
            </CardHeader>
            <CardContent className="space-y-2">
                {loading ? (
                    <div className="flex justify-center py-6">
                        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                    </div>
                ) : error ? (
                    <div className="py-4 px-2 text-center space-y-2">
                        <p className="text-sm text-muted-foreground">{t("dashboard.vehiclePosition.loadError")}</p>
                        <button type="button" onClick={onRetry} className="inline-flex items-center gap-1.5 text-xs font-medium text-primary hover:underline">
                            <RefreshCw className="h-3.5 w-3.5" />
                            {t("dashboard.vehiclePosition.retry")}
                        </button>
                    </div>
                ) : vehicles.length === 0 ? (
                    <p className="text-sm text-muted-foreground py-4 text-center">{t("dashboard.vehiclePosition.noVehicles")}</p>
                ) : (
                    <ul className="space-y-2 max-h-[280px] overflow-y-auto">
                        {vehicles.map((v) => (
                            <li key={v.truckId} className="flex items-center justify-between gap-2 p-2 rounded-lg bg-muted/40 hover:bg-muted/60 transition-colors">
                                <div className="min-w-0 flex-1">
                                    <p className="text-sm font-medium text-foreground truncate flex items-center gap-1.5">
                                        {v.engineOn === true ? "🟢" : "⚪"}
                                        {v.licensePlate?.trim() || "—"}
                                    </p>
                                    <p className="text-xs text-muted-foreground truncate mt-0.5">
                                        {v.positionDescription?.trim() || (v.lastGpsAt ? t("dashboard.vehiclePosition.lastFix", { time: timeAgo(v.lastGpsAt, t) }) : "")}
                                    </p>
                                </div>
                                <a
                                    href={`https://www.google.com/maps?q=${v.lat},${v.lng}`}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className="shrink-0 inline-flex items-center gap-1 text-xs text-primary hover:underline"
                                    title={`${v.lat.toFixed(5)}, ${v.lng.toFixed(5)}`}
                                >
                                    <MapPin className="h-3.5 w-3.5" />
                                    {t("dashboard.vehiclePosition.openMap")}
                                    <ExternalLink className="h-3 w-3" />
                                </a>
                            </li>
                        ))}
                    </ul>
                )}
            </CardContent>
        </Card>
    );
}

// --- counters from ['badges'] ----------------------------------------------------------------------

export function ExpenseAuditCard({ pending }: { pending: number }) {
    const { t } = useLanguage();
    return (
        <Card className="shadow-sm" data-testid="expense-audit">
            <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                <CardTitle className="text-sm font-semibold flex items-center gap-2">
                    <ClipboardCheck className="h-4 w-4 text-emerald-600" />
                    {t("dashboard.expenses.title")}
                </CardTitle>
                <Link href="/app/accounting/audit" className="text-xs text-primary hover:underline flex items-center gap-1">
                    {t("dashboard.vehiclePosition.viewAll")} <ArrowRight className="h-3 w-3" />
                </Link>
            </CardHeader>
            <CardContent>
                <div className="flex items-center justify-between">
                    <div className="space-y-1">
                        <p className="text-3xl font-bold tracking-tight tabular-nums">{pending.toLocaleString()}</p>
                        <p className="text-xs text-muted-foreground">{t("dashboard.expenses.pendingApproval")}</p>
                    </div>
                    <div className={`p-3 rounded-full ${pending > 0 ? "bg-amber-100 dark:bg-amber-950 text-amber-600" : "bg-muted text-muted-foreground"}`}>
                        <ClipboardCheck className="h-6 w-6" />
                    </div>
                </div>
            </CardContent>
        </Card>
    );
}

export function ChatStatusCard({ unassigned, urgent }: { unassigned: number; urgent: number }) {
    const { t } = useLanguage();
    const row = (icon: LucideIcon, tone: string, title: string, subtitle: string, value: number, testId: string) => {
        const Icon = icon;
        return (
            <Link href="/app/chat" className="flex items-center gap-3 p-3 rounded-lg bg-muted/50 hover:bg-muted transition-colors" data-testid={testId}>
                <div className={`p-2 rounded-full ${tone}`}>
                    <Icon className="h-4 w-4" />
                </div>
                <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium text-foreground">{title}</p>
                    <p className="text-xs text-muted-foreground">{subtitle}</p>
                </div>
                <span className="text-lg font-bold tabular-nums">{value}</span>
            </Link>
        );
    };
    return (
        <Card className="bg-card border border-border" data-testid="chat-status">
            <CardHeader className="pb-2">
                <div className="flex items-center justify-between">
                    <h3 className="text-sm font-semibold text-foreground">{t("dashboard.chats.title")}</h3>
                    <Link href="/app/chat" className="text-xs font-medium text-primary hover:underline">
                        {t("dashboard.chats.viewAll")}
                    </Link>
                </div>
            </CardHeader>
            <CardContent className="space-y-3">
                {row(MessageCircle, "bg-amber-500/10 text-amber-600 dark:text-amber-400", t("dashboard.chats.pending"), t("dashboard.chats.unassigned"), unassigned, "chats-unassigned")}
                {row(AlertTriangle, "bg-red-500/10 text-red-600 dark:text-red-400", t("dashboard.chats.urgent"), t("dashboard.chats.needsAttention"), urgent, "chats-urgent")}
            </CardContent>
        </Card>
    );
}

// --- recent updates (summary.recent) ---------------------------------------------------------------

const RECENT_STYLE: Record<RecentItemDTO["type"], { icon: LucideIcon; bg: string; color: string; link: string }> = {
    delivered: { icon: Truck, bg: "bg-green-500/10", color: "text-green-500", link: "/app/driver-monitor" },
    driver: { icon: CheckCircle, bg: "bg-blue-500/10", color: "text-blue-500", link: "/app/drivers" },
    incident: { icon: AlertTriangle, bg: "bg-yellow-500/10", color: "text-yellow-500", link: "/app/incident-reports" },
    maintenance: { icon: Wrench, bg: "bg-gray-500/10", color: "text-gray-500", link: "/app/maintenance" },
};

/** The title and the detail line of one feed entry (the old widget's wording). */
export function recentText(item: RecentItemDTO, t: Translate): { title: string; detail: string } {
    const type = t(`dashboard.updates.type.${item.type}`);
    switch (item.type) {
        case "delivered":
            return { title: item.plate ? `${type} · ${item.plate}` : type, detail: item.place ?? "" };
        case "driver":
            return { title: type, detail: item.name ?? "" };
        case "incident":
            return { title: type, detail: (item.text ?? "").slice(0, 40) };
        case "maintenance":
            return { title: type, detail: (item.text ?? "").slice(0, 30) };
    }
}

export function RecentUpdatesFeed({ items }: { items: readonly RecentItemDTO[] }) {
    const { t } = useLanguage();
    return (
        <div className="bg-card border border-border rounded-xl p-6 shadow-sm" data-testid="recent-updates">
            <h3 className="text-lg font-semibold text-foreground mb-1">{t("dashboard.updates.title")}</h3>
            <p className="text-sm text-muted-foreground mb-6">{t("dashboard.updates.subtitle")}</p>
            {items.length === 0 ? (
                <p className="text-sm text-muted-foreground py-4">{t("dashboard.updates.noActivity")}</p>
            ) : (
                <div className="space-y-6">
                    {items.map((item) => {
                        const style = RECENT_STYLE[item.type] ?? RECENT_STYLE.delivered;
                        const { title, detail } = recentText(item, t);
                        const Icon = style.icon;
                        return (
                            <Link key={`${item.type}-${item.id}`} href={style.link} className="block hover:bg-muted/50 rounded-lg -m-2 p-2 transition-colors">
                                <div className="flex gap-4">
                                    <div className={`p-3 rounded-full h-fit ${style.bg} ${style.color}`}>
                                        <Icon className="w-5 h-5" />
                                    </div>
                                    <div>
                                        <h4 className="font-medium text-sm text-foreground">{title}</h4>
                                        <p className="text-xs text-muted-foreground mt-0.5">{[timeAgo(item.at, t), detail].filter(Boolean).join(" · ")}</p>
                                    </div>
                                </div>
                            </Link>
                        );
                    })}
                </div>
            )}
        </div>
    );
}

// --- compliance (summary.compliance) ---------------------------------------------------------------

const KIND_STYLE: Record<ComplianceItemDTO["kind"], { icon: LucideIcon; bg: string; color: string }> = {
    tax: { icon: AlertTriangle, bg: "bg-orange-100", color: "text-orange-600" },
    insurance: { icon: ShieldAlert, bg: "bg-blue-100", color: "text-blue-600" },
    service_date: { icon: CalendarDays, bg: "bg-purple-100", color: "text-purple-600" },
    service_km: { icon: Wrench, bg: "bg-purple-100", color: "text-purple-600" },
};

/** `yyyy-MM-dd` as dd/MM/yyyy (the old widget's en-GB form), with no time zone shift. */
function dayText(date: string): string {
    const [y, m, d] = date.split("-");
    return `${d}/${m}/${y}`;
}

function DueBadge({ item }: { item: ComplianceItemDTO }) {
    const { t } = useLanguage();
    if (item.kmRemaining !== undefined) {
        const overdue = item.kmRemaining < 0;
        return (
            <Badge variant={item.kmRemaining < 500 ? "destructive" : "secondary"} className={overdue ? "bg-red-600 hover:bg-red-700" : ""}>
                {overdue
                    ? t("dashboard.compliance.kmOverdue", { km: Math.abs(item.kmRemaining).toLocaleString() })
                    : t("dashboard.compliance.kmLeft", { km: item.kmRemaining.toLocaleString() })}
            </Badge>
        );
    }
    if (item.daysRemaining !== undefined) {
        const overdue = item.daysRemaining < 0;
        return (
            <Badge variant={item.daysRemaining < 7 ? "destructive" : "secondary"} className={overdue ? "bg-red-600 hover:bg-red-700" : ""}>
                {overdue ? t("dashboard.compliance.daysOverdue", { days: Math.abs(item.daysRemaining) }) : t("dashboard.compliance.daysLeft", { days: item.daysRemaining })}
            </Badge>
        );
    }
    return null;
}

export function ComplianceCard({ compliance }: { compliance: ComplianceBlockDTO }) {
    const { t } = useLanguage();
    if (compliance.itemsTotal === 0) {
        return (
            <Card className="bg-green-50/50 border-green-100" data-testid="compliance">
                <CardHeader className="pb-2">
                    <CardTitle className="text-lg font-medium flex items-center gap-2 text-green-700">
                        <CheckCircle2 className="h-5 w-5" />
                        {t("dashboard.compliance.statusTitle")}
                    </CardTitle>
                </CardHeader>
                <CardContent>
                    <p className="text-sm text-green-600">{t("dashboard.compliance.allClear")}</p>
                </CardContent>
            </Card>
        );
    }
    const hidden = compliance.itemsTotal - compliance.items.length;
    return (
        <Card className="border-red-100 shadow-sm" data-testid="compliance">
            <CardHeader className="pb-3 border-b border-red-50 bg-red-50/30">
                <CardTitle className="text-lg font-medium flex items-center gap-2 text-red-700">
                    <ShieldAlert className="h-5 w-5" />
                    {t("dashboard.compliance.actionRequired")}
                    <Badge variant="destructive" className="ml-auto">
                        {t("dashboard.compliance.alerts", { count: compliance.itemsTotal })}
                    </Badge>
                </CardTitle>
            </CardHeader>
            <CardContent className="p-0">
                <div className="divide-y divide-red-50 max-h-[400px] overflow-y-auto">
                    {compliance.items.map((item) => {
                        const style = KIND_STYLE[item.kind] ?? KIND_STYLE.tax;
                        const Icon = style.icon;
                        return (
                            <Link
                                key={`${item.truckId}-${item.kind}`}
                                href={`/app/trucks/edit?id=${encodeURIComponent(item.truckId)}`}
                                prefetch={false}
                                className="flex items-center justify-between p-4 hover:bg-muted/50 transition-colors"
                            >
                                <div className="flex items-start gap-3">
                                    <div className={`mt-0.5 p-1.5 rounded-full ${style.bg}`}>
                                        <Icon className={`h-4 w-4 ${style.color}`} />
                                    </div>
                                    <div className="space-y-1">
                                        <p className="text-sm font-medium leading-none">{item.licensePlate}</p>
                                        <div className="text-xs text-muted-foreground flex items-center gap-1">
                                            <span className="font-semibold">{t(`dashboard.compliance.kind.${item.kind}`)}</span>
                                            {item.dueDate && <span>{t("dashboard.compliance.dueOn", { date: dayText(item.dueDate) })}</span>}
                                        </div>
                                    </div>
                                </div>
                                <div className="text-right">
                                    <DueBadge item={item} />
                                </div>
                            </Link>
                        );
                    })}
                    {hidden > 0 && <p className="p-4 text-xs text-muted-foreground">{t("dashboard.compliance.more", { count: hidden })}</p>}
                </div>
            </CardContent>
        </Card>
    );
}
