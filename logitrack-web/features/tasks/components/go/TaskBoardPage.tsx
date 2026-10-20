"use client";

/**
 * The first-mile, line-haul and job-assign boards over Go (T34, P2; Appendix E §E.2.1, §E.5 rows 5, 9, 11).
 *
 * One page, three routes: `['tasks',{date,type,soc,hub}]` (`GET /v1/tasks`, keyset 200 per page,
 * `keepPreviousData` while a filter changes) with the day, the destination SOC and the source hub as the server's
 * filters (W9: a busy day's SOCN tasks past row 200 are on the first page); the customer scope is RLS. The source
 * hub picker offers every hub of the Go master (TW8, the shared `HubFilterCombobox`), not the loaded rows' sources.
 * The task form, the detail and the import are mounted on open only (no hubs -> trucks -> drivers -> customers
 * waterfall on mount,
 * `useFirstMileTask.ts:66-106`); their pickers are cached queries (`['hubs',{view:'go'}]`, `['customers',
 * {fields:'minimal'}]`) and server searches (drivers, trucks). SSE `task.*` refreshes the board in every tab
 * (500 ms window), so a mobile check-in shows without a reload.
 */
import { useMemo, useState } from "react";
import dynamic from "next/dynamic";
import { keepPreviousData, useInfiniteQuery } from "@tanstack/react-query";
import { format } from "date-fns";
import { Calendar as CalendarIcon, Copy, FileSpreadsheet, MoreHorizontal, Plus, RefreshCw } from "lucide-react";
import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";
import { useCan, useMe } from "@/features/auth/api/useMe";
import { navigationProfileOf, profileRouteLabelKey, type NavigationProfile } from "@/features/auth/utils/navigationProfile";
import { ApiErrorAlert, formatDateTime, formatDay } from "@/features/accounting/components/go/shared";
import { apiErrorText } from "@/lib/apiError";
import { cn } from "@/lib/utils";
import { useIsMobile } from "@/hooks/use-mobile";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

import { HubFilterCombobox } from "@/features/hubs/components/HubFilterCombobox";
import {
    BOARD_ALL,
    boardTime,
    cancelTask,
    pageItems,
    taskBoardQuery,
    type TaskBoardType,
    type TaskDTO,
    type TaskType,
} from "../../api/tasks.go";
import { CapabilityGate, goHubFilterOptions, LoadMoreFooter, TaskStatusBadge, useGoHubs, VehicleClassChip } from "./opsShared";
import type { TaskFormMode } from "./TaskFormDialog";

// Mounted on open only, each its own chunk (developer-spec.md §10.11): the form and its pickers, the detail with
// the trip editor, the import with xlsx.
const TaskFormDialog = dynamic(() => import("./TaskFormDialog").then((m) => m.TaskFormDialog), { ssr: false });
const TaskDetailDialog = dynamic(() => import("./TaskDetailDialog").then((m) => m.TaskDetailDialog), { ssr: false });
const TaskImportDialog = dynamic(() => import("./TaskImportDialog").then((m) => m.TaskImportDialog), { ssr: false });

const SOC_FILTERS = ["SOCE", "SOCN", "SOCW"] as const;

type BoardKind = TaskBoardType;

const CAPABILITY: Record<BoardKind, string> = {
    first_mile: "operations:view_first_mile",
    line_haul: "operations:view_line_haul",
    all: "operations:view_first_mile",
};

export function FirstMileBoardPage() {
    return (
        <CapabilityGate capability={CAPABILITY.first_mile}>
            <Board kind="first_mile" />
        </CapabilityGate>
    );
}

export function LineHaulBoardPage() {
    return (
        <CapabilityGate capability={CAPABILITY.line_haul}>
            <Board kind="line_haul" />
        </CapabilityGate>
    );
}

export function JobAssignBoardPage() {
    return (
        <CapabilityGate capability={CAPABILITY.all} anyOf={["operations:view_line_haul"]}>
            <Board kind="all" />
        </CapabilityGate>
    );
}

/** The board's words; its title is the menu entry's label (UI3B: one name in the menu, the crumb and the H1). */
function titleKeys(kind: BoardKind, profile: NavigationProfile): { title: string; subtitle: string; add: string } {
    switch (kind) {
        case "line_haul":
            return { title: "nav.lineHaulTasks", subtitle: "lineHaul.subtitle", add: "lineHaul.newAssignment" };
        case "all":
            return { title: profileRouteLabelKey("/app/job-assign", profile) ?? "nav.jobAssign", subtitle: profile === "staff" ? "jobAssign.subtitle" : "nav.scopedTasksDescription", add: "jobAssign.addJob" };
        default:
            return { title: "nav.firstMileTasks", subtitle: "firstMile.subtitle", add: "firstMile.newAssignment" };
    }
}

interface FormState {
    mode: TaskFormMode;
    taskType: TaskType;
    task?: TaskDTO;
}

function Board({ kind }: { kind: BoardKind }) {
    const isMobile = useIsMobile();
    const { t } = useLanguage();
    const { data: profile = "staff" } = useMe(navigationProfileOf);
    const canManage = useCan("operations:manage_tasks").allowed;
    const [date, setDate] = useState<Date | undefined>(() => new Date());
    const [typeFilter, setTypeFilter] = useState<TaskBoardType>("all");
    const [soc, setSoc] = useState<string>(BOARD_ALL);
    const [hub, setHub] = useState<string>(BOARD_ALL);
    const [form, setForm] = useState<FormState | null>(null);
    const [detail, setDetail] = useState<TaskDTO | null>(null);
    const [cancelling, setCancelling] = useState<TaskDTO | null>(null);
    const [busy, setBusy] = useState(false);
    const [importOpen, setImportOpen] = useState(false);

    const type: TaskBoardType = kind === "all" ? typeFilter : kind;
    const filter = { date: date ? format(date, "yyyy-MM-dd") : "", type, soc, hub };
    const q = useInfiniteQuery({ ...taskBoardQuery(filter), placeholderData: keepPreviousData });
    const shown = useMemo(() => pageItems(q.data?.pages), [q.data]);
    const hubs = useGoHubs();
    const hubOptions = useMemo(() => goHubFilterOptions(hubs.list), [hubs.list]);
    const keys = titleKeys(kind, profile);
    const defaultType: TaskType = kind === "line_haul" ? "line_haul" : "first_mile";

    const confirmCancel = async () => {
        if (!cancelling) return;
        setBusy(true);
        try {
            await cancelTask(cancelling.id, undefined);
            toast.success(t("taskBoard.cancelled"));
            setCancelling(null);
        } catch (e) {
            toast.error(apiErrorText(e, t));
        } finally {
            setBusy(false);
        }
    };

    const colSpan = kind === "all" ? 13 : 12;
    return (
        <div className="flex flex-1 flex-col gap-section">
            <PageHeader
                title={t(keys.title)}
                description={t(keys.subtitle)}
                actions={
                    <>
                        <Button variant="outline" size="icon" onClick={() => void q.refetch()} aria-label={t("firstMile.sources.refresh")}>
                            <RefreshCw className={cn("h-4 w-4", q.isFetching && "animate-spin")} aria-hidden />
                        </Button>
                        {canManage ? (
                            <>
                                <Button variant="outline" className="gap-2" onClick={() => setImportOpen(true)}>
                                    <FileSpreadsheet className="h-4 w-4" aria-hidden />
                                    {t("firstMile.import.button")}
                                </Button>
                                {kind === "all" ? (
                                    <DropdownMenu>
                                        <DropdownMenuTrigger asChild>
                                            <Button>
                                                <Plus className="mr-2 h-4 w-4" aria-hidden />
                                                {t("jobAssign.addJob")}
                                            </Button>
                                        </DropdownMenuTrigger>
                                        <DropdownMenuContent align="end">
                                            <DropdownMenuItem onClick={() => setForm({ mode: "create", taskType: "first_mile" })}>{t("jobAssign.addFirstMile")}</DropdownMenuItem>
                                            <DropdownMenuItem onClick={() => setForm({ mode: "create", taskType: "line_haul" })}>{t("jobAssign.addLineHaul")}</DropdownMenuItem>
                                        </DropdownMenuContent>
                                    </DropdownMenu>
                                ) : (
                                    <Button onClick={() => setForm({ mode: "create", taskType: defaultType })}>
                                        <Plus className="mr-2 h-4 w-4" aria-hidden />
                                        {t(keys.add)}
                                    </Button>
                                )}
                            </>
                        ) : null}
                    </>
                }
            />

            <Card>
                <CardHeader className="pb-3">
                    <CardTitle className="text-lg">{t("firstMile.filters")}</CardTitle>
                </CardHeader>
                <CardContent className="flex flex-wrap gap-4">
                    <div className="flex flex-col gap-2">
                        <label className="text-sm font-medium">{t("firstMile.filter.date")}</label>
                        <div className="flex gap-2">
                            <Popover>
                                <PopoverTrigger asChild>
                                    <Button variant="outline" className={cn("w-[200px] justify-start text-left font-normal", !date && "text-muted-foreground")}>
                                        <CalendarIcon className="mr-2 h-4 w-4" />
                                        {date ? format(date, "dd/MM/yyyy") : t("firstMile.filter.pickDate")}
                                    </Button>
                                </PopoverTrigger>
                                <PopoverContent className="w-auto p-0" align="start">
                                    <Calendar mode="single" selected={date} onSelect={setDate} initialFocus />
                                </PopoverContent>
                            </Popover>
                            {date ? (
                                <Button variant="ghost" size="sm" onClick={() => setDate(undefined)} title={t("taskBoard.anyDayHint")}>
                                    {t("taskBoard.anyDay")}
                                </Button>
                            ) : null}
                        </div>
                    </div>
                    {kind === "all" ? (
                        <div className="flex min-w-[160px] flex-col gap-2">
                            <label className="text-sm font-medium">{t("jobAssign.filter.type")}</label>
                            <Select value={typeFilter} onValueChange={(v) => setTypeFilter(v as TaskBoardType)}>
                                <SelectTrigger>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="all">{t("jobAssign.filter.allTypes")}</SelectItem>
                                    <SelectItem value="first_mile">{t("jobAssign.addFirstMile")}</SelectItem>
                                    <SelectItem value="line_haul">{t("jobAssign.addLineHaul")}</SelectItem>
                                </SelectContent>
                            </Select>
                        </div>
                    ) : null}
                    <div className="flex min-w-[200px] flex-col gap-2">
                        <label className="text-sm font-medium">{t("firstMile.filter.destination")}</label>
                        <Select value={soc} onValueChange={setSoc}>
                            <SelectTrigger>
                                <SelectValue placeholder={t("firstMile.filter.allSOCs")} />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("firstMile.filter.allSOCs")}</SelectItem>
                                {SOC_FILTERS.map((k) => (
                                    <SelectItem key={k} value={k}>
                                        {k}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="flex min-w-[220px] flex-1 flex-col gap-2">
                        <label className="text-sm font-medium">{t("firstMile.filter.sourceHub")}</label>
                        <HubFilterCombobox options={hubOptions} value={hub} onChange={setHub} allLabel={t("firstMile.filter.allHubs")} />
                    </div>
                </CardContent>
            </Card>

            <ApiErrorAlert error={q.error} />

            {isMobile ? <div className="divide-y rounded-md border bg-card md:hidden" aria-label={t(keys.title)}>
                {q.isPending ? <p className="p-4 text-sm text-muted-foreground">{t("taskBoard.loading")}</p> : shown.length === 0 ? (
                    <p className="p-4 text-sm">{t("firstMile.table.noTasks")}</p>
                ) : shown.map((task) => (
                    <article key={task.id} className="space-y-3 p-4" aria-label={task.taskNo ?? task.source?.label ?? task.sourceHub}>
                        <div className="flex flex-wrap items-start justify-between gap-2">
                            <p className="min-w-0 break-words font-medium">{task.taskNo ?? (task.taskType === "line_haul" ? t("jobAssign.addLineHaul") : t("jobAssign.addFirstMile"))}</p>
                            <TaskStatusBadge status={task.status} />
                        </div>
                        <p className="break-words text-sm">{task.source?.label || task.sourceHub} → {task.destinationPlace?.label || task.destination}</p>
                        <p className="text-sm text-muted-foreground">{formatDay(task.planDate)} · {boardTime(task) || "—"}</p>
                        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-sm">
                            <dt className="text-muted-foreground">{t("firstMile.table.driver")}</dt>
                            <dd className="break-words">{task.driverName ?? "—"}{task.helperName ? ` + ${task.helperName}` : ""}</dd>
                            <dt className="text-muted-foreground">{t("firstMile.table.licensePlate")}</dt>
                            <dd className="break-words">{task.licensePlate ?? "—"}</dd>
                        </dl>
                        <Button variant="outline" className="min-h-11 w-full" onClick={() => setDetail(task)} aria-label={`${t("common.view")} ${task.taskNo ?? task.source?.label ?? task.sourceHub}`}>
                            {t("common.view")}
                        </Button>
                    </article>
                ))}
            </div> : (
            <div className="hidden overflow-hidden rounded-md border bg-card md:block">
                <div className="overflow-x-auto">
                    <Table>
                        <TableHeader className="bg-muted/50">
                            <TableRow>
                                <TableHead>{t("firstMile.table.date")}</TableHead>
                                {kind === "all" ? <TableHead>{t("jobAssign.table.jobType")}</TableHead> : null}
                                <TableHead>{t("firstMile.table.sourceHub")}</TableHead>
                                <TableHead>{t("firstMile.table.destination")}</TableHead>
                                <TableHead>{t("firstMile.table.time")}</TableHead>
                                <TableHead>{t("firstMile.table.type")}</TableHead>
                                <TableHead>{t("firstMile.table.shipmentId")}</TableHead>
                                <TableHead>{t("firstMile.table.licensePlate")}</TableHead>
                                <TableHead>{t("firstMile.table.driver")}</TableHead>
                                <TableHead>{t("taskBoard.table.trip")}</TableHead>
                                <TableHead>{t("taskBoard.table.status")}</TableHead>
                                <TableHead>{t("taskBoard.table.checkIn")}</TableHead>
                                <TableHead className="text-right">{t("firstMile.table.actions")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {q.isPending ? (
                                <TableRow>
                                    <TableCell colSpan={colSpan} className="h-24 text-center text-muted-foreground">
                                        {t("taskBoard.loading")}
                                    </TableCell>
                                </TableRow>
                            ) : shown.length === 0 ? (
                                <TableRow>
                                    <TableCell colSpan={colSpan} className="h-24 text-center">
                                        {t("firstMile.table.noTasks")}
                                    </TableCell>
                                </TableRow>
                            ) : (
                                shown.map((task) => (
                                    <TableRow key={task.id} className="cursor-pointer hover:bg-muted/50" onClick={() => setDetail(task)}>
                                        <TableCell>{formatDay(task.planDate)}</TableCell>
                                        {kind === "all" ? (
                                            <TableCell>
                                                <Badge variant="secondary">{task.taskType === "line_haul" ? t("jobAssign.addLineHaul") : t("jobAssign.addFirstMile")}</Badge>
                                            </TableCell>
                                        ) : null}
                                        <TableCell className="max-w-[220px]">
                                            <span className="block font-medium leading-tight">{task.source?.label || task.sourceHub}</span>
                                        </TableCell>
                                        <TableCell>
                                            <Badge variant="outline" className="font-normal">
                                                {task.destinationPlace?.label || task.destination}
                                            </Badge>
                                            {task.isMultiDelivery && task.stops.length > 1 ? (
                                                <span className="ml-1 text-xs text-muted-foreground">{t("taskBoard.stops", { count: task.stops.length })}</span>
                                            ) : null}
                                        </TableCell>
                                        <TableCell>{boardTime(task) || "—"}</TableCell>
                                        <TableCell>
                                            <VehicleClassChip value={task.truckType} />
                                        </TableCell>
                                        <TableCell className="font-mono text-sm"><Button variant="link" className="h-auto px-0 max-md:min-h-11" onClick={(event) => { event.stopPropagation(); setDetail(task); }} aria-label={`${t("common.view")} ${task.taskNo ?? task.source?.label ?? task.sourceHub}`}>
                                            {task.taskNo ?? t("common.view")}
                                        </Button></TableCell>
                                        <TableCell className="font-mono">{task.licensePlate ?? "—"}</TableCell>
                                        <TableCell>
                                            {task.driverName ?? "—"}
                                            {task.helperName ? <span className="block text-xs text-muted-foreground">+ {task.helperName}</span> : null}
                                        </TableCell>
                                        <TableCell className="font-mono text-xs">{task.trip ? task.trip.tripNo : "—"}</TableCell>
                                        <TableCell>
                                            <TaskStatusBadge status={task.status} />
                                        </TableCell>
                                        <TableCell className="text-sm">
                                            {task.checkInAt ? (
                                                <span className="flex flex-col gap-0.5">
                                                    <span>{formatDateTime(task.checkInAt)}</span>
                                                    {task.checkInLat != null && task.checkInLng != null ? (
                                                        <span className="text-xs text-muted-foreground">
                                                            {task.checkInLat.toFixed(4)}, {task.checkInLng.toFixed(4)}
                                                        </span>
                                                    ) : null}
                                                </span>
                                            ) : (
                                                <span className="text-muted-foreground">—</span>
                                            )}
                                        </TableCell>
                                        <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                                            {canManage ? (
                                                <DropdownMenu>
                                                    <DropdownMenuTrigger asChild>
                                                        <Button variant="ghost" size="icon" className="h-8 w-8" aria-label={t("firstMile.table.actions")}>
                                                            <MoreHorizontal className="h-4 w-4" />
                                                        </Button>
                                                    </DropdownMenuTrigger>
                                                    <DropdownMenuContent align="end">
                                                        <DropdownMenuItem onClick={() => setForm({ mode: "edit", taskType: task.taskType, task })}>
                                                            {task.status === "cancelled" ? t("taskBoard.reassign") : t("firstMile.table.edit")}
                                                        </DropdownMenuItem>
                                                        <DropdownMenuItem onClick={() => setForm({ mode: "duplicate", taskType: task.taskType, task })}>
                                                            <Copy className="mr-2 h-4 w-4" />
                                                            {t("jobAssign.duplicate")}
                                                        </DropdownMenuItem>
                                                        {task.status !== "cancelled" && task.status !== "completed" ? (
                                                            <DropdownMenuItem className="text-destructive focus:text-destructive" onClick={() => setCancelling(task)}>
                                                                {t("taskBoard.cancel")}
                                                            </DropdownMenuItem>
                                                        ) : null}
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
            </div>)}
                <LoadMoreFooter
                    loaded={shown.length}
                    hasNextPage={Boolean(q.hasNextPage)}
                    isFetchingNextPage={q.isFetchingNextPage}
                    onLoadMore={() => void q.fetchNextPage()}
                />

            {form ? (
                <TaskFormDialog
                    mode={form.mode}
                    taskType={form.taskType}
                    task={form.task}
                    allowTypeSwitch={kind === "all" && form.mode !== "edit"}
                    onClose={() => setForm(null)}
                />
            ) : null}
            {detail ? (
                <TaskDetailDialog
                    task={detail}
                    canManage={canManage}
                    onClose={() => setDetail(null)}
                    onEdit={(task) => {
                        setDetail(null);
                        setForm({ mode: "edit", taskType: task.taskType, task });
                    }}
                />
            ) : null}
            {importOpen ? <TaskImportDialog kind={kind} onClose={() => setImportOpen(false)} /> : null}

            <Dialog open={!!cancelling} onOpenChange={(o) => !o && !busy && setCancelling(null)}>
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>{t("taskBoard.cancelTitle")}</DialogTitle>
                        <DialogDescription>
                            {t("taskBoard.cancelMessage", { taskNo: cancelling?.taskNo ?? cancelling?.id ?? "" })}
                        </DialogDescription>
                    </DialogHeader>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => setCancelling(null)} disabled={busy}>
                            {t("taskBoard.cancelAbort")}
                        </Button>
                        <Button variant="destructive" onClick={() => void confirmCancel()} disabled={busy}>
                            {t("taskBoard.cancelConfirm")}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}
