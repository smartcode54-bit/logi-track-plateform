"use client";

/**
 * Holidays over Go (T46, issue #66; Appendix E §E.2.2, §E.5 row 6), the only source since TW7.
 *
 * The page reads one Bangkok year at a time, `['holidays', year]` (`GET /v1/holidays?year`), a plain fetch
 * (10 min, no listener and no poll): the year the calendar shows (FullCalendar's `datesSet`) or the list's
 * year. The key holds no language, so a language toggle re-renders the names (`nameEn` / `nameTh`) and
 * reads nothing (Appendix E §E.3.5; the legacy listener re-subscribed on every toggle). `holidays.changed`
 * makes `['holidays']` stale while the stream is open, every write of this page does too.
 *
 * Writes are Go routes (no callable): add (`POST`, upsert on day and type), edit (`PATCH` of the changed
 * fields), delete behind a confirmation (`DELETE`), and "Generate holidays": the Thai public holidays of a
 * year from `shared-docs/logic/thaiHolidays.ts`, reviewed, then `PUT /v1/holidays/generate`. Public holidays
 * are a steward's (R60): other principals see them read-only and cannot generate. FullCalendar loads after
 * the page, in its own chunk (developer-spec.md §10.11).
 */
import { useMemo, useRef, useState } from "react";
import dynamic from "next/dynamic";
import { useQuery, keepPreviousData } from "@tanstack/react-query";
import { format } from "date-fns";
import { th as thLocale } from "date-fns/locale";
import type FullCalendar from "@fullcalendar/react";
import {
    Calendar as CalendarIcon,
    Calendar as CalendarViewIcon,
    ChevronLeft,
    ChevronRight,
    LayoutGrid,
    List as ListViewIcon,
    Loader2,
    MoreHorizontal,
    Pencil,
    Plus,
    Search,
    Sparkles,
    Trash2,
} from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { useMe } from "@/features/auth/api/useMe";
import { HrErrorAlert } from "@/features/driver-compensation/components/go/hrShared";
import { cn } from "@/lib/utils";
import { toneBadgeVariant } from "@/lib/statusPresentation";
import { PageHeader } from "@/components/page-header";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { generateThaiPublicHolidays } from "shared-docs/logic/thaiHolidays";
import type { Holiday } from "@/validate/holidaySchema";

import {
    holidayDescription,
    holidayName,
    holidaysQuery,
    useDeleteHoliday,
    useGenerateHolidays,
    type GeneratedHoliday,
    type HolidayDTO,
    type HolidayType,
} from "../api/holidays.go";
import type { HolidayCalendarProps } from "./HolidayCalendar";
import { HolidayDialog } from "./HolidayDialog";
import { ReviewGeneratedDialog } from "./ReviewGeneratedDialog";
import { YearSelectDialog } from "./YearSelectDialog";

// FullCalendar loads after the page, in its own chunk (developer-spec.md §10.11).
const HolidayCalendar = dynamic(() => import("./HolidayCalendar"), {
    ssr: false,
    loading: () => <Skeleton className="h-[640px] w-full rounded-lg" />,
});

type View = "year" | "month" | "list";

// The callback arguments, from the calendar's own props (the `@fullcalendar/core` types are not a direct dependency).
type ArgOf<K extends keyof HolidayCalendarProps> = Parameters<Extract<NonNullable<HolidayCalendarProps[K]>, (...args: never[]) => unknown>>[0];
type DatesSetArg = ArgOf<"datesSet">;
type DateClickArg = ArgOf<"dateClick">;
type EventClickArg = ArgOf<"eventClick">;
type DayCellContentArg = ArgOf<"dayCellContent">;

const TYPE_BADGE = {
    public: toneBadgeVariant("primary"),
    company: "outline",
    other: "secondary",
} as const;
const TYPE_COLOR: Record<string, string> = { public: "var(--primary)", company: "var(--chart-2)", other: "var(--muted-foreground)" };

const todayKey = () => format(new Date(), "yyyy-MM-dd");

/** A generated holiday of the review (legacy shape, local-midnight `Date`) as `PUT /v1/holidays/generate` takes it. */
export function toGenerated(h: Partial<Holiday>): GeneratedHoliday | null {
    if (!(h.date instanceof Date) || Number.isNaN(h.date.getTime())) return null;
    const nameEn = h.holidayNameEN?.trim() || undefined;
    const nameTh = h.holidayNameTH?.trim() || undefined;
    return {
        date: format(h.date, "yyyy-MM-dd"),
        type: (h.type ?? "PUBLIC").toLowerCase() as HolidayType,
        name: h.name?.trim() || [nameEn, nameTh].filter(Boolean).join(" ") || "",
        ...(nameEn ? { nameEn } : {}),
        ...(nameTh ? { nameTh } : {}),
        isRecurring: Boolean(h.isRecurring),
    };
}

export function HolidaysPage() {
    const { t, language } = useLanguage();
    const me = useMe();
    const steward = me.data?.steward === true;
    const tenantId = me.data?.tenant?.id ?? null;

    const [view, setView] = useState<View>("month");
    const [year, setYear] = useState(() => new Date().getFullYear());
    const [listFilter, setListFilter] = useState<"month" | "year">("year");
    const [search, setSearch] = useState("");
    const calendarRef = useRef<FullCalendar>(null);

    const holidays = useQuery({ ...holidaysQuery(year), placeholderData: keepPreviousData });
    const rows = useMemo(() => holidays.data ?? [], [holidays.data]);
    const byDay = useMemo(() => {
        const m = new Map<string, HolidayDTO>();
        for (const h of rows) if (!m.has(h.date)) m.set(h.date, h);
        return m;
    }, [rows]);

    const [dialog, setDialog] = useState<{ edit: HolidayDTO | null; day?: string } | null>(null);
    const [deleting, setDeleting] = useState<HolidayDTO | null>(null);
    const [yearSelectOpen, setYearSelectOpen] = useState(false);
    const [generated, setGenerated] = useState<{ year: number; list: Partial<Holiday>[] } | null>(null);
    const generate = useGenerateHolidays();

    /** A steward writes every holiday; anyone else the company and other holidays of their own tenant. */
    const canEdit = (h: HolidayDTO) => steward || (h.tenantId !== null && h.tenantId === tenantId);

    const today = todayKey();
    const stats = useMemo(
        () => ({ total: rows.length, upcoming: rows.filter((h) => h.date >= today).length, recurring: rows.filter((h) => h.isRecurring).length }),
        [rows, today]
    );

    const filtered = useMemo(() => {
        const q = search.trim().toLowerCase();
        const month = today.slice(0, 7);
        return rows.filter((h) => {
            if (q) {
                const hay = [h.name, h.nameEn ?? "", h.nameTh ?? "", h.type].join(" ").toLowerCase();
                if (!hay.includes(q)) return false;
            }
            return listFilter === "month" ? h.date.startsWith(month) : true;
        });
    }, [rows, search, listFilter, today]);

    const events = useMemo(
        () =>
            rows.map((h) => ({
                id: h.id,
                title: holidayName(h, language),
                start: h.date,
                allDay: true,
                backgroundColor: `color-mix(in srgb, ${TYPE_COLOR[h.type] ?? TYPE_COLOR.other} 12%, var(--card))`,
                borderColor: "var(--border)",
                textColor: "var(--foreground)",
                extendedProps: { holidayId: h.id },
                display: "block",
            })),
        [rows, language]
    );

    const changeView = (next: View) => {
        setView(next);
        if (next !== "list") {
            setTimeout(() => {
                const api = calendarRef.current?.getApi();
                if (api) {
                    api.today();
                    api.changeView(next === "year" ? "multiMonthYear" : "dayGridMonth");
                }
            }, 0);
        }
    };

    /** Shows a year: the list's year, or the calendar moved there (its `datesSet` then reads the year). */
    const goToYear = (y: number) => {
        const api = view === "list" ? undefined : calendarRef.current?.getApi();
        if (api) api.gotoDate(`${y}-01-01`);
        else setYear(y);
    };

    const onDatesSet = (arg: DatesSetArg) => {
        const y = arg.view.currentStart.getFullYear();
        setYear((cur) => (cur === y ? cur : y));
    };

    const onDateClick = (arg: DateClickArg) => setDialog({ edit: null, day: arg.dateStr });
    const onEventClick = (arg: EventClickArg) => {
        const h = rows.find((r) => r.id === arg.event.id);
        if (h && canEdit(h)) setDialog({ edit: h });
    };

    const onYearSelected = (y: number) => {
        try {
            setGenerated({ year: y, list: generateThaiPublicHolidays(y) });
        } catch {
            toast.error(t("holidays.toast.failedGenerate"));
        }
    };

    const saveGenerated = async (finalHolidays: Partial<Holiday>[], initialHolidays: Partial<Holiday>[]) => {
        if (!generated) return;
        try {
            const out = await generate.mutateAsync({
                year: generated.year,
                finalHolidays: finalHolidays.map(toGenerated).filter((h): h is GeneratedHoliday => h !== null),
                initialHolidays: initialHolidays.map(toGenerated).filter((h): h is GeneratedHoliday => h !== null),
            });
            toast.success(t("hr.holidays.generated", { saved: out.saved, deleted: out.deleted, duplicates: out.duplicates }));
            setGenerated(null);
            goToYear(generated.year);
        } catch (error) {
            toast.error(`${t("holidays.toast.failedSave")}: ${errorMessage(error, t)}`);
            throw error;
        }
    };

    return (
        <TooltipProvider>
            <div className="flex flex-col gap-section">
                <PageHeader
                    title={t("nav.holidays")}
                    description={t("holidays.manageHolidays")}
                    actions={
                        <>
                            {steward ? (
                                <Button variant="outline" onClick={() => setYearSelectOpen(true)} disabled={generate.isPending}>
                                    {generate.isPending ? (
                                        <Loader2 className="mr-2 h-4 w-4 animate-spin" aria-hidden />
                                    ) : (
                                        <Sparkles className="mr-2 h-4 w-4" aria-hidden />
                                    )}
                                    {t("holidays.generateHolidays")}
                                </Button>
                            ) : null}
                            <div className="flex items-center gap-1 rounded-xl border border-border/50 bg-muted/50 p-1">
                                {(
                                    [
                                        ["year", LayoutGrid, "holidays.viewYear"],
                                        ["month", CalendarViewIcon, "holidays.viewMonth"],
                                        ["list", ListViewIcon, "holidays.viewList"],
                                    ] as const
                                ).map(([v, Icon, key]) => (
                                    <Button
                                        key={v}
                                        variant="ghost"
                                        size="sm"
                                        onClick={() => changeView(v)}
                                        aria-pressed={view === v}
                                        className={cn(
                                            "h-8 rounded-lg px-4 text-xs font-semibold transition-all duration-200",
                                            view === v
                                                ? "bg-background text-foreground shadow-sm hover:bg-background hover:text-foreground"
                                                : "text-muted-foreground hover:bg-muted hover:text-foreground"
                                        )}
                                    >
                                        <Icon className="mr-2 h-3.5 w-3.5" aria-hidden />
                                        {t(key)}
                                    </Button>
                                ))}
                            </div>
                            <Button onClick={() => setDialog({ edit: null })}>
                                <Plus className="mr-2 h-4 w-4" aria-hidden />
                                {t("holidays.addHoliday")}
                            </Button>
                        </>
                    }
                />

                <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
                    <StatCard label={t("hr.holidays.totalInYear", { year })} value={stats.total} testId="holidays-total" />
                    <StatCard label={t("holidays.upcoming")} value={stats.upcoming} testId="holidays-upcoming" />
                    <StatCard label={t("holidays.recurring")} value={stats.recurring} testId="holidays-recurring" />
                </div>

                <HrErrorAlert error={holidays.error} title={t("holidays.toast.failedLoad")} />

                {view === "list" ? (
                    <>
                        <div className="flex flex-col items-center gap-4 rounded-xl border border-border/50 bg-card/50 p-4 shadow-sm md:flex-row">
                            <div className="relative w-full max-w-sm flex-1">
                                <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
                                <Input placeholder={t("holidays.searchPlaceholder")} className="h-10 rounded-lg pl-10" value={search} onChange={(e) => setSearch(e.target.value)} />
                            </div>
                            <div className="flex items-center gap-1">
                                <Button variant="ghost" size="icon" aria-label={t("hr.holidays.previousYear")} onClick={() => setYear((y) => y - 1)}>
                                    <ChevronLeft className="h-4 w-4" />
                                </Button>
                                <span className="min-w-[4rem] text-center text-sm font-semibold tabular-nums" data-testid="holidays-year">
                                    {year}
                                </span>
                                <Button variant="ghost" size="icon" aria-label={t("hr.holidays.nextYear")} onClick={() => setYear((y) => y + 1)}>
                                    <ChevronRight className="h-4 w-4" />
                                </Button>
                            </div>
                            <div className="flex w-full items-center gap-2 rounded-lg border border-border/50 bg-muted/50 p-1 md:w-auto">
                                <Button
                                    variant={listFilter === "month" ? "secondary" : "ghost"}
                                    size="sm"
                                    onClick={() => setListFilter("month")}
                                    className="h-8 flex-1 px-3 text-xs font-semibold md:flex-none"
                                >
                                    {t("holidays.thisMonth")}
                                </Button>
                                <Button
                                    variant={listFilter === "year" ? "secondary" : "ghost"}
                                    size="sm"
                                    onClick={() => setListFilter("year")}
                                    className="h-8 flex-1 px-3 text-xs font-semibold md:flex-none"
                                >
                                    {t("hr.holidays.wholeYear")}
                                </Button>
                            </div>
                        </div>

                        <div className="overflow-hidden rounded-xl border bg-card shadow-sm">
                            <Table className="w-full table-fixed">
                                <TableHeader className="bg-muted/30">
                                    <TableRow>
                                        <TableHead className="w-12 text-center font-semibold">{t("holidays.table.no")}</TableHead>
                                        <TableHead className="w-2/5 min-w-0 font-semibold">{t("holidays.table.name")}</TableHead>
                                        <TableHead className="w-1/4 font-semibold">{t("holidays.table.date")}</TableHead>
                                        <TableHead className="w-1/5 font-semibold">{t("holidays.table.type")}</TableHead>
                                        <TableHead className="w-20 text-right font-semibold">{t("holidays.table.actions")}</TableHead>
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {holidays.isPending ? (
                                        <TableRow>
                                            <TableCell colSpan={5} className="h-32 text-center">
                                                <Loader2 className="mx-auto h-6 w-6 animate-spin text-muted-foreground" />
                                            </TableCell>
                                        </TableRow>
                                    ) : filtered.length === 0 ? (
                                        <TableRow>
                                            <TableCell colSpan={5} className="h-32 text-center text-muted-foreground">
                                                {t("holidays.noHolidays")}
                                            </TableCell>
                                        </TableRow>
                                    ) : (
                                        filtered.map((h, i) => (
                                            <TableRow key={h.id} className="hover:bg-muted/20">
                                                <TableCell className="w-12 text-center tabular-nums text-muted-foreground">{i + 1}</TableCell>
                                                <TableCell className="w-2/5 min-w-0 whitespace-normal break-words font-semibold">{holidayName(h, language)}</TableCell>
                                                <TableCell className="w-1/4">{format(new Date(`${h.date}T00:00:00`), "PPP", { locale: language === "th" ? thLocale : undefined })}</TableCell>
                                                <TableCell className="w-1/5">
                                                    <Badge variant={TYPE_BADGE[h.type] ?? TYPE_BADGE.other}>
                                                        {t(`holidays.type.${h.type}`)}
                                                    </Badge>
                                                </TableCell>
                                                <TableCell className="w-20 text-right">
                                                    {canEdit(h) ? (
                                                        <DropdownMenu>
                                                            <DropdownMenuTrigger asChild>
                                                                <Button variant="ghost" size="icon" className="h-8 w-8" aria-label={`${t("holidays.table.actions")} ${holidayName(h, language)}`}>
                                                                    <MoreHorizontal className="h-4 w-4" />
                                                                </Button>
                                                            </DropdownMenuTrigger>
                                                            <DropdownMenuContent align="end" className="w-36">
                                                                <DropdownMenuLabel>{t("common.actions")}</DropdownMenuLabel>
                                                                <DropdownMenuItem onClick={() => setDialog({ edit: h })}>
                                                                    <Pencil className="mr-2 h-3.5 w-3.5" /> {t("holidays.editHoliday")}
                                                                </DropdownMenuItem>
                                                                <DropdownMenuItem className="text-destructive focus:text-destructive" onClick={() => setDeleting(h)}>
                                                                    <Trash2 className="mr-2 h-3.5 w-3.5" /> {t("holidays.deleteHoliday")}
                                                                </DropdownMenuItem>
                                                            </DropdownMenuContent>
                                                        </DropdownMenu>
                                                    ) : null}
                                                </TableCell>
                                            </TableRow>
                                        ))
                                    )}
                                </TableBody>
                            </Table>
                        </div>
                    </>
                ) : (
                    <Card className="border-border bg-card p-4">
                        <div className="calendar-container">
                            <HolidayCalendar
                                calendarRef={calendarRef}
                                initialView={view === "year" ? "multiMonthYear" : "dayGridMonth"}
                                initialDate={`${year}-${today.slice(5, 7)}-01`}
                                headerToolbar={{ left: "prev,next today", center: "title", right: "" }}
                                events={events}
                                datesSet={onDatesSet}
                                dateClick={onDateClick}
                                eventClick={onEventClick}
                                height="auto"
                                themeSystem="standard"
                                multiMonthMaxColumns={view === "year" ? 3 : 1}
                                eventClassNames="cursor-pointer hover:opacity-80 transition-opacity font-medium text-xs px-1"
                                dayMaxEvents={10}
                                fixedWeekCount={false}
                                showNonCurrentDates={view !== "month"}
                                dayCellClassNames={(arg: DayCellContentArg) => {
                                    const classes = ["transition-all duration-200"];
                                    if (byDay.has(format(arg.date, "yyyy-MM-dd"))) classes.push("holiday-cell");
                                    const day = arg.date.getDay();
                                    if (day === 0) classes.push("weekend-sun");
                                    if (day === 6) classes.push("weekend-sat");
                                    return classes;
                                }}
                                dayCellContent={(arg: DayCellContentArg) => {
                                    const h = byDay.get(format(arg.date, "yyyy-MM-dd"));
                                    const content = (
                                        <div className="group flex cursor-default items-center gap-1.5 px-2 py-1">
                                            <span className={cn("text-sm font-medium transition-colors", h ? "font-semibold text-primary" : "text-foreground")}>
                                                {arg.dayNumberText}
                                            </span>
                                            {h ? <div className="h-1.5 w-1.5 rounded-full bg-primary" /> : null}
                                        </div>
                                    );
                                    if (view !== "year" || !h) return content;
                                    const description = holidayDescription(h, language);
                                    return (
                                        <Tooltip delayDuration={100}>
                                            <TooltipTrigger asChild>{content}</TooltipTrigger>
                                            <TooltipContent side="top" className="overflow-hidden border border-border p-0 shadow-md">
                                                <div className="w-64 bg-card text-card-foreground">
                                                    <div className="border-b border-border bg-muted px-3 py-1.5">
                                                        <p className="text-xs font-medium text-foreground">
                                                            {t("holidays.table.type")}: {t(`holidays.type.${h.type}`)}
                                                        </p>
                                                    </div>
                                                    <div className="space-y-2 p-3">
                                                        <p className="text-base font-semibold leading-tight">{holidayName(h, language)}</p>
                                                        {description ? <p className="border-t border-border pt-1 text-xs italic leading-relaxed text-muted-foreground">{description}</p> : null}
                                                    </div>
                                                </div>
                                            </TooltipContent>
                                        </Tooltip>
                                    );
                                }}
                                eventDidMount={(arg) => {
                                    arg.el.setAttribute("title", arg.event.title);
                                }}
                            />
                        </div>
                    </Card>
                )}
            </div>

            <HolidayDialog open={dialog !== null} onOpenChange={(o) => !o && setDialog(null)} edit={dialog?.edit ?? null} initialDate={dialog?.day} steward={steward} />
            <DeleteHolidayDialog holiday={deleting} onClose={() => setDeleting(null)} />
            <YearSelectDialog open={yearSelectOpen} onOpenChange={setYearSelectOpen} onSelect={onYearSelected} />
            <ReviewGeneratedDialog
                open={generated !== null}
                onOpenChange={(o) => !o && setGenerated(null)}
                holidays={generated?.list ?? EMPTY}
                onSave={saveGenerated}
            />
        </TooltipProvider>
    );
}

const EMPTY: Partial<Holiday>[] = [];

function errorMessage(error: unknown, t: (k: string, f?: string | Record<string, string | number>) => string): string {
    return error instanceof Error && error.message ? error.message : t("apiError.unknown");
}

function StatCard({ label, value, testId }: { label: string; value: number; testId: string }) {
    return (
        <Card className="border-border/60 shadow-sm">
            <CardContent className="flex items-center justify-between p-6">
                <div>
                    <p className="text-sm font-medium text-muted-foreground">{label}</p>
                    <h2 className="text-3xl font-semibold" data-testid={testId}>
                        {value}
                    </h2>
                </div>
                <CalendarIcon className="h-8 w-8 text-muted-foreground" />
            </CardContent>
        </Card>
    );
}

function DeleteHolidayDialog({ holiday, onClose }: { holiday: HolidayDTO | null; onClose: () => void }) {
    const { t, language } = useLanguage();
    const remove = useDeleteHoliday();
    const [error, setError] = useState<unknown>(null);
    const confirm = async () => {
        if (!holiday) return;
        setError(null);
        try {
            await remove.mutateAsync({ id: holiday.id });
            toast.success(t("holidays.toast.deleted"));
            onClose();
        } catch (e) {
            setError(e);
        }
    };
    return (
        <Dialog
            open={holiday !== null}
            onOpenChange={(o) => {
                if (!o && !remove.isPending) {
                    setError(null);
                    onClose();
                }
            }}
        >
            <DialogContent>
                <DialogHeader>
                    <DialogTitle>{t("holidays.deleteHoliday")}</DialogTitle>
                    <DialogDescription>
                        {t("holidays.deleteConfirm")} {holiday ? `(${holidayName(holiday, language)}, ${holiday.date})` : ""}
                    </DialogDescription>
                </DialogHeader>
                <HrErrorAlert error={error} />
                <DialogFooter>
                    <Button variant="outline" onClick={onClose} disabled={remove.isPending}>
                        {t("holidays.dialog.cancel")}
                    </Button>
                    <Button variant="destructive" onClick={() => void confirm()} disabled={remove.isPending}>
                        {remove.isPending ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("holidays.deleteHoliday")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
