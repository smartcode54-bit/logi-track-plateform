"use client";

/**
 * Pieces the Go operations pages share (T34): the capability gate (no query runs before `['me']` grants the
 * route), the hub and SOC picker over `['hubs',{view:'go'}]`, the status badges and the "load more" footer of a
 * keyset list. Nothing here imports Firebase, and nothing renders a key of a split namespace (`driverMonitor`,
 * `accounting`), so the standby and incident routes (outside the driver-monitor group) can use it.
 */
import { useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, ChevronsUpDown, Loader2, ShieldX } from "lucide-react";

import { useLanguage } from "@/context/language";
import { useCan } from "@/features/auth/api/useMe";
import { hubPicksQuery, type HubPick } from "@/features/accounting/api/pickers.go";
import { fieldViolations } from "@/features/auth/utils/fieldErrors";
import { apiErrorText } from "@/lib/apiError";
import { fieldReasonText } from "@/lib/formServerErrors";
import { cn } from "@/lib/utils";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";

import type { TaskStatus } from "../../api/tasks.go";

/**
 * Renders `children` only once `['me']` grants `capability` (a catalog key): the page's queries never start for a
 * principal the route does not admit (Appendix E §E.2.1: "All queries `enabled` only when `['me']` grants the route
 * capability"). The edge gate (`proxy.ts`) already refuses the route; this covers a tab whose claims changed.
 */
export function CapabilityGate({ capability, anyOf, children }: { capability: string; anyOf?: string[]; children: ReactNode }) {
    const { t } = useLanguage();
    const main = useCan(capability);
    const alt0 = useCan(anyOf?.[0] ?? capability);
    const alt1 = useCan(anyOf?.[1] ?? capability);
    const loading = main.loading;
    const allowed = main.allowed || alt0.allowed || alt1.allowed;
    if (loading) {
        return (
            <div className="flex h-[50vh] items-center justify-center" role="status" aria-busy="true">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
            </div>
        );
    }
    if (!allowed) {
        return (
            <div className="flex h-[50vh] flex-col items-center justify-center gap-3 text-center">
                <ShieldX className="h-12 w-12 text-destructive/60" />
                <p className="text-lg font-semibold">{t("unauthorized.title")}</p>
                <p className="max-w-md text-sm text-muted-foreground">{t("unauthorized.description")}</p>
            </div>
        );
    }
    return <>{children}</>;
}

/** Every hub and SOC from Go (`['hubs',{view:'go'}]`, 10 min; `hubs.changed` reaches it through `['hubs']`). */
export function useGoHubs(enabled = true) {
    const q = useQuery({ ...hubPicksQuery, enabled });
    const list = useMemo(() => [...(q.data ?? [])].sort((a, b) => a.sourceId.localeCompare(b.sourceId)), [q.data]);
    const byCode = useMemo(() => new Map(list.map((h) => [h.sourceId.toUpperCase(), h])), [list]);
    return { ...q, list, byCode };
}

/**
 * The source-hub filter options of the Go boards (TW8: every hub of the Go master, no cap; the value is the hub
 * code the board sends as Go's `sourceHub=code:<CODE>`): Thai name first, searchable by code and both names.
 */
export function goHubFilterOptions(hubs: readonly HubPick[]): { value: string; label: string; searchText: string }[] {
    const seen = new Set<string>();
    const out: { value: string; label: string; searchText: string }[] = [];
    for (const h of hubs) {
        const code = h.sourceId?.trim();
        if (!code || seen.has(code)) continue;
        seen.add(code);
        const th = h.sourceNameTh?.trim() ?? "";
        const en = h.sourceNameEn?.trim() ?? "";
        out.push({ value: code, label: th || en || code, searchText: [code, en, th].filter(Boolean).join(" ") });
    }
    return out;
}

export function isSocStation(h: Pick<HubPick, "stationType">): boolean {
    return String(h.stationType ?? "").trim().toUpperCase().startsWith("SOC");
}

export function hubDisplay(h: Pick<HubPick, "sourceId" | "sourceNameTh" | "sourceNameEn">): string {
    const name = h.sourceNameTh?.trim() || h.sourceNameEn?.trim();
    return name && name !== h.sourceId ? `${h.sourceId} — ${name}` : h.sourceId;
}

interface HubComboboxProps {
    hubs: HubPick[];
    /** The hub or SOC code (the task's `sourceHub` / `destination`). */
    value: string;
    onChange: (code: string, hub: HubPick | null) => void;
    /** Which stations come first: hubs (first-mile source, line-haul destination) or SOCs. */
    prefer?: "hub" | "soc";
    placeholder: string;
    className?: string;
    disabled?: boolean;
    id?: string;
}

/**
 * A searchable hub / SOC picker over the Go hub master: every station, the preferred kind first, no slice (the
 * legacy dropdowns showed the first 50, TW8). A code that is not in the master (a legacy task) stays shown.
 */
export function HubCombobox({ hubs, value, onChange, prefer = "hub", placeholder, className, disabled, id }: HubComboboxProps) {
    const { t } = useLanguage();
    const [open, setOpen] = useState(false);
    const [term, setTerm] = useState("");
    const ordered = useMemo(() => {
        const first = hubs.filter((h) => (prefer === "soc" ? isSocStation(h) : !isSocStation(h)));
        const rest = hubs.filter((h) => !first.includes(h));
        return [...first, ...rest];
    }, [hubs, prefer]);
    const q = term.trim().toLowerCase();
    const shown = q ? ordered.filter((h) => hubDisplay(h).toLowerCase().includes(q)) : ordered;
    const current = hubs.find((h) => h.sourceId === value);
    return (
        <Popover open={open} onOpenChange={setOpen}>
            <PopoverTrigger asChild>
                <Button id={id} type="button" variant="outline" role="combobox" aria-expanded={open} disabled={disabled}
                    className={cn("w-full justify-between font-normal", className)}>
                    <span className={cn("truncate", !value && "text-muted-foreground")}>{current ? hubDisplay(current) : value || placeholder}</span>
                    <ChevronsUpDown className="ml-2 h-4 w-4 shrink-0 opacity-50" />
                </Button>
            </PopoverTrigger>
            <PopoverContent className="z-[1005] w-[320px] p-2" align="start">
                <Input autoFocus value={term} onChange={(e) => setTerm(e.target.value)} placeholder={t("common.searchPlaceholder")} className="mb-2 h-8" />
                <div className="max-h-72 overflow-y-auto" role="listbox">
                    {shown.length === 0 ? <p className="px-2 py-1.5 text-xs text-muted-foreground">{t("common.noResults")}</p> : null}
                    {shown.map((h) => (
                        <button key={h.id} type="button" role="option" aria-selected={h.sourceId === value}
                            className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm hover:bg-accent"
                            onClick={() => {
                                onChange(h.sourceId, h);
                                setOpen(false);
                                setTerm("");
                            }}>
                            <Check className={cn("h-4 w-4 shrink-0", h.sourceId === value ? "opacity-100" : "opacity-0")} />
                            <span className="truncate">{hubDisplay(h)}</span>
                            {isSocStation(h) ? <Badge variant="outline" className="ml-auto text-[10px]">SOC</Badge> : null}
                        </button>
                    ))}
                </div>
            </PopoverContent>
        </Popover>
    );
}

const TASK_STATUS_STYLE: Record<TaskStatus, string> = {
    pending: "bg-slate-500/10 text-slate-700 border-slate-500/25 dark:text-slate-300",
    assigned: "bg-blue-500/10 text-blue-700 border-blue-500/25 dark:text-blue-300",
    checked_in: "bg-teal-500/10 text-teal-700 border-teal-500/25 dark:text-teal-300",
    in_transit: "bg-amber-500/15 text-amber-700 border-amber-500/25 dark:text-amber-300",
    completed: "bg-emerald-500/15 text-emerald-700 border-emerald-500/25 dark:text-emerald-300",
    cancelled: "bg-zinc-500/15 text-zinc-500 border-zinc-500/25",
};

export function TaskStatusBadge({ status }: { status: TaskStatus | string }) {
    const { t } = useLanguage();
    return (
        <Badge variant="outline" className={cn("font-medium", TASK_STATUS_STYLE[status as TaskStatus])}>
            {t(`taskBoard.status.${status}`, status)}
        </Badge>
    );
}

const VEHICLE_CLASS_STYLE: Record<string, string> = {
    "4W": "bg-green-50 text-green-700 ring-green-600/20",
    "4WJ": "bg-cyan-50 text-cyan-700 ring-cyan-600/20",
    "6WH": "bg-purple-50 text-purple-700 ring-purple-600/20",
    "10WH": "bg-gray-50 text-gray-600 ring-gray-500/10",
    "18WH": "bg-orange-50 text-orange-700 ring-orange-600/20",
    VAN: "bg-indigo-50 text-indigo-700 ring-indigo-600/20",
};

export function VehicleClassChip({ value }: { value: string | null | undefined }) {
    if (!value) return <span className="text-muted-foreground">—</span>;
    return (
        <span className={cn("inline-flex items-center rounded-md px-2 py-1 text-xs font-medium ring-1 ring-inset", VEHICLE_CLASS_STYLE[value] ?? "bg-muted")}>
            {value}
        </span>
    );
}

/** The footer of a keyset list: rows loaded, and the next page on demand (no silent cap, TW8). */
export function LoadMoreFooter({
    loaded,
    hasNextPage,
    isFetchingNextPage,
    onLoadMore,
}: {
    loaded: number;
    hasNextPage: boolean;
    isFetchingNextPage: boolean;
    onLoadMore: () => void;
}) {
    const { t } = useLanguage();
    return (
        <div className="flex items-center justify-between gap-2 border-t px-4 py-3 text-sm text-muted-foreground">
            <span>{t("taskBoard.loadedRows", { count: loaded })}</span>
            {hasNextPage ? (
                <Button variant="outline" size="sm" onClick={onLoadMore} disabled={isFetchingNextPage}>
                    {isFetchingNextPage ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                    {t("taskBoard.loadMore")}
                </Button>
            ) : (
                <span>{t("taskBoard.allLoaded")}</span>
            )}
        </div>
    );
}

/** `datetime-local` value of an RFC 3339 instant in the browser's zone ("" for none). */
export function toLocalInput(iso: string | null | undefined): string {
    if (!iso) return "";
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return "";
    const pad = (n: number) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** An RFC 3339 instant of a `datetime-local` value (null for ""). */
export function fromLocalInput(v: string): string | null {
    if (!v) return null;
    const d = new Date(v);
    return Number.isNaN(d.getTime()) ? null : d.toISOString();
}

/**
 * An error of a Go write, failing loudly (ADR 0003): the catalogue text and, for a `422 invalid_argument`, each
 * field the server named with its reason in the active language.
 */
export function WriteErrorAlert({ error }: { error: unknown }) {
    const { t } = useLanguage();
    if (!error) return null;
    const fields = fieldViolations(error);
    return (
        <Alert variant="destructive">
            <AlertDescription>
                <p>{apiErrorText(error, t)}</p>
                {fields.length > 0 ? (
                    <ul className="mt-1 list-disc pl-5 text-xs">
                        {fields.map((f, i) => (
                            <li key={`${f.field}-${i}`}>
                                <span className="font-mono">{f.field}</span>: {fieldReasonText(f.reason, t, f.params)}
                            </li>
                        ))}
                    </ul>
                ) : null}
            </AlertDescription>
        </Alert>
    );
}
