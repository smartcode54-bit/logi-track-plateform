"use client";

import { useState, useEffect, useMemo, useCallback } from "react";
import { endOfDay, format, startOfDay } from "date-fns";
import { Calendar as CalendarIcon, Plus, MoreHorizontal, Pencil, RefreshCw, Copy, Settings2 } from "lucide-react";
import { JobImportDialog } from "./import-dialog";
import { FirstMileTaskDialog } from "@/app/app/first-mile/task-dialog";
import { LineHaulTaskDialog } from "@/app/app/line-haul/task-dialog";
import { useLanguage } from "@/context/language";
import { useCustomerScope } from "@/hooks/useCustomerScope";
import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";
import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { SOC_DESTINATIONS, SOC_KEYS, Task as JobTask, normalizeSocIdToKey } from "@/validate/taskSchema";
import { buildHubCodeToDisplayMapFromHubRows, resolveHubOrSocDisplay } from "@/lib/hubDisplay";
import { collection, getDocs, onSnapshot, query, orderBy, limit, doc, updateDoc, where, type QueryConstraint } from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
import { db, functions } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import {
    DropdownMenu,
    DropdownMenuCheckboxItem,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";
import { EditTripDetailsDialog } from "@/app/app/driver-monitor/EditTripDetailsDialog";
import type { TripRecord } from "@/validate/tripRecordSchema";
import { useDriverNamesByAuthId } from "@/hooks/useDriverNamesByAuthId";

type TaskTypeFilter = "all" | "FIRST_MILE" | "LINE_HAUL";

/** Columns the Job Assigning table can show, toggled from the column chooser (Combolist). */
const JOB_COLUMNS: { key: string; labelKey: string; fallback: string }[] = [
    { key: "date", labelKey: "firstMile.task.date", fallback: "วันแผนงาน" },
    { key: "jobType", labelKey: "jobAssign.table.jobType", fallback: "ชนิดงาน" },
    { key: "source", labelKey: "firstMile.table.sourceHub", fallback: "ต้นทาง" },
    { key: "destination", labelKey: "firstMile.table.destination", fallback: "ปลายทาง" },
    { key: "actualPickup", labelKey: "jobAssign.table.actualPickup", fallback: "วันรับงานจริง" },
    { key: "truckType", labelKey: "firstMile.table.type", fallback: "ประเภทรถ" },
    { key: "taskId", labelKey: "firstMile.table.shipmentId", fallback: "รหัสงาน" },
    { key: "licensePlate", labelKey: "firstMile.table.licensePlate", fallback: "ทะเบียน" },
    { key: "driver", labelKey: "firstMile.table.driver", fallback: "คนขับ" },
    { key: "status", labelKey: "firstMile.table.status", fallback: "สถานะ" },
    { key: "checkIn", labelKey: "lineHaul.table.checkIn", fallback: "เช็คอิน" },
];
const DEFAULT_VISIBLE_COLS = ["date", "jobType", "source", "destination", "actualPickup", "truckType", "taskId", "licensePlate", "driver", "status", "checkIn"];
const COLS_STORAGE_KEY = "jobAssign.visibleColumns";

function toDate(val: unknown): Date | null {
    if (!val) return null;
    if (val instanceof Date) return val;
    if (typeof (val as { toDate?: () => Date })?.toDate === "function") return (val as { toDate: () => Date }).toDate();
    if (typeof val === "string") return new Date(val);
    return null;
}

export default function JobAssignPage() {
    const { t } = useLanguage();
    const { customerScopeId, isCustomer } = useCustomerScope();
    const [date, setDate] = useState<Date | undefined>(new Date());
    const [taskTypeFilter, setTaskTypeFilter] = useState<TaskTypeFilter>("all");
    const [tasks, setTasks] = useState<JobTask[]>([]);
    const [hubs, setHubs] = useState<Record<string, any>[]>([]);
    const [selectedHub, setSelectedHub] = useState<string>("all");
    const [selectedSOC, setSelectedSOC] = useState<string>("all");

    // Dialog state — dialogTaskType picks which task form (FM vs LH) is mounted.
    const [isDialogOpen, setIsDialogOpen] = useState(false);
    const [dialogMode, setDialogMode] = useState<"create" | "edit">("create");
    const [dialogTaskType, setDialogTaskType] = useState<"FIRST_MILE" | "LINE_HAUL">("FIRST_MILE");
    const [selectedTask, setSelectedTask] = useState<Partial<JobTask> | undefined>(undefined);
    const [cancelTask, setCancelTask] = useState<JobTask | null>(null);
    const [detailTask, setDetailTask] = useState<JobTask | null>(null);
    const [detailTrip, setDetailTrip] = useState<TripRecord | null>(null);
    const [editTripDialogOpen, setEditTripDialogOpen] = useState(false);

    const helperNames = useDriverNamesByAuthId(detailTask?.helperDriverIds);

    // Column chooser (Combolist) — which table columns are visible, remembered per browser.
    const [visibleCols, setVisibleCols] = useState<string[]>(DEFAULT_VISIBLE_COLS);
    useEffect(() => {
        try {
            const saved = localStorage.getItem(COLS_STORAGE_KEY);
            if (saved) {
                const parsed = JSON.parse(saved);
                if (Array.isArray(parsed) && parsed.length) setVisibleCols(parsed.filter((k) => JOB_COLUMNS.some((c) => c.key === k)));
            }
        } catch { /* ignore */ }
    }, []);
    const showCol = (key: string) => visibleCols.includes(key);
    const toggleCol = (key: string) =>
        setVisibleCols((prev) => {
            const next = prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key];
            try { localStorage.setItem(COLS_STORAGE_KEY, JSON.stringify(next)); } catch { /* ignore */ }
            return next;
        });
    const orderedVisibleCols = JOB_COLUMNS.filter((c) => showCol(c.key));

    const fetchHubs = async () => {
        try {
            const snap = await getDocs(collection(db, "hubs"));
            const hubList = snap.docs.map((d) => {
                const data = d.data();
                return {
                    "Hub Code": data.source_id ?? data.hubId ?? data.hubCode,
                    "Hub Name": data.source_name_en ?? data.hubName,
                    "Hub Name Th":
                        (data.source_name_th ?? data.hubTHName ?? data.hub_th_name ?? data.station_name_th ?? "") || undefined,
                    linkedCustomerName: data.linkedCustomerName,
                    station_type: data.station_type,
                    source: "custom",
                    id: d.id,
                };
            });
            setHubs(hubList);
        } catch (err) {
            console.error("Failed to fetch hubs", err);
        }
    };

    useEffect(() => {
        fetchHubs();
    }, []);

    // Fetch the linked trip_record when a task detail opens.
    useEffect(() => {
        if (!detailTask) {
            setDetailTrip(null);
            return;
        }
        const taskIds = [detailTask.id, detailTask.taskId].filter(Boolean) as string[];
        if (taskIds.length === 0) {
            setDetailTrip(null);
            return;
        }
        const q = query(collection(db, COLLECTIONS.TRIP_RECORDS), where("taskId", "in", taskIds), limit(1));
        getDocs(q)
            .then((snap) => {
                const d = snap.docs[0];
                if (!d) {
                    setDetailTrip(null);
                    return;
                }
                const data = d.data();
                setDetailTrip({
                    id: d.id,
                    ...data,
                    createdAt: toDate(data.createdAt) ?? undefined,
                    updatedAt: toDate(data.updatedAt) ?? undefined,
                    deliveredTimestamp: toDate(data.deliveredTimestamp) ?? undefined,
                } as TripRecord);
            })
            .catch(() => setDetailTrip(null));
    }, [detailTask?.id, detailTask?.taskId]);

    // Tasks for the selected day (both task types unless a type filter is chosen). The date filter
    // drives the Firestore query directly (same reasoning as the old FM/LH pages): matching client-side
    // on a truncated recent list silently hid days past the window.
    useEffect(() => {
        const constraints: QueryConstraint[] = [];
        if (taskTypeFilter !== "all") constraints.push(where("taskType", "==", taskTypeFilter));
        if (date) {
            constraints.push(where("date", ">=", startOfDay(date)), where("date", "<=", endOfDay(date)), orderBy("date", "desc"));
        } else {
            constraints.push(orderBy("createdAt", "desc"), limit(150));
        }
        const q = query(collection(db, COLLECTIONS.TASKS), ...constraints);
        const unsub = onSnapshot(
            q,
            (snapshot) => {
                const fetched: JobTask[] = snapshot.docs.map((d) => {
                    const data = d.data();
                    return {
                        id: d.id,
                        ...data,
                        date: data.date?.toDate?.() ?? data.date,
                        actualPickupAt: data.actualPickupAt?.toDate?.() ?? data.actualPickupAt,
                        createdAt: data.createdAt?.toDate?.() ?? data.createdAt,
                        updatedAt: data.updatedAt?.toDate?.() ?? data.updatedAt,
                        checkInAt: data.checkInAt?.toDate?.() ?? data.checkInAt,
                    };
                }) as JobTask[];
                fetched.sort((a, b) => (toDate(b.createdAt)?.getTime() ?? 0) - (toDate(a.createdAt)?.getTime() ?? 0));
                setTasks(fetched);
            },
            (err) => console.error("Failed to load tasks:", err)
        );
        return () => unsub();
    }, [date, taskTypeFilter]);

    const hubDisplayMap = useMemo(() => buildHubCodeToDisplayMapFromHubRows(hubs), [hubs]);
    const routeLabel = useCallback(
        (code: string | null | undefined) => resolveHubOrSocDisplay(code ?? null, hubDisplayMap),
        [hubDisplayMap]
    );

    /** Cell content for one column key — the header/body both iterate `orderedVisibleCols`. */
    const renderCell = (task: JobTask, key: string) => {
        switch (key) {
            case "date": return task.date ? format(task.date as Date, "dd/MM/yyyy") : "-";
            case "jobType": return (
                <Badge variant="outline" className={cn(task.taskType === "LINE_HAUL" ? "border-purple-300 text-purple-700" : "border-cyan-300 text-cyan-700")}>
                    {task.taskType === "LINE_HAUL" ? "LH" : "FM"}
                </Badge>
            );
            case "source": return <span className="font-medium leading-tight block">{routeLabel(task.sourceHub)}</span>;
            case "destination": return <span className="font-medium leading-tight block">{routeLabel(task.destination)}</span>;
            case "actualPickup": return task.actualPickupAt ? format(task.actualPickupAt as Date, "dd/MM/yyyy HH:mm") : <span className="text-muted-foreground">-</span>;
            case "truckType": return (
                <span className="inline-flex items-center rounded-md px-2 py-1 text-xs font-medium ring-1 ring-inset bg-muted text-foreground/80 ring-border">
                    {task.truckType}
                </span>
            );
            case "taskId": return <span className="font-mono text-sm">{task.taskId}</span>;
            case "licensePlate": return <span className="font-mono">{task.licensePlate}</span>;
            case "driver": return task.driverName;
            case "status": return (
                <Badge variant={task.status === "Cancelled" ? "secondary" : task.status === "Checked in" || task.status === "Completed" ? "default" : "outline"}>
                    {task.status}
                </Badge>
            );
            case "checkIn": return task.checkInAt ? (
                <span className="flex flex-col gap-0.5 text-sm">
                    <span>{task.checkInAt instanceof Date ? format(task.checkInAt as Date, "dd/MM/yyyy HH:mm") : "-"}</span>
                    {task.checkInPhotoUrl && (
                        <a href={task.checkInPhotoUrl} target="_blank" rel="noopener noreferrer" className="text-primary underline text-xs" onClick={(e) => e.stopPropagation()}>{t("lineHaul.viewPhoto", "ดูรูป")}</a>
                    )}
                    {task.checkInLat != null && task.checkInLng != null && (
                        <span className="text-muted-foreground text-xs">{task.checkInLat.toFixed(4)}, {task.checkInLng.toFixed(4)}</span>
                    )}
                </span>
            ) : <span className="text-muted-foreground">-</span>;
            default: return null;
        }
    };

    const openCreate = (taskType: "FIRST_MILE" | "LINE_HAUL") => {
        setDialogTaskType(taskType);
        setDialogMode("create");
        setSelectedTask(undefined);
        setIsDialogOpen(true);
    };

    /** In-form task-type switch: swap the mounted form and start it fresh (FM/LH source+destination differ). */
    const handleDialogTaskTypeChange = (next: "FIRST_MILE" | "LINE_HAUL") => {
        setSelectedTask(undefined);
        setDialogTaskType(next);
    };

    const openEdit = (task: JobTask) => {
        setDialogTaskType(task.taskType === "LINE_HAUL" ? "LINE_HAUL" : "FIRST_MILE");
        setDialogMode("edit");
        setSelectedTask(task);
        setIsDialogOpen(true);
    };

    /** Duplicate a job into a NEW create form: keep route / customer / vehicle, drop identity + status. */
    const openDuplicate = (task: JobTask) => {
        const seed: Partial<JobTask> = {
            taskType: task.taskType,
            sourceHub: task.sourceHub,
            destination: task.destination,
            time: task.time,
            jobCategory: task.jobCategory,
            truckType: task.truckType,
            truckId: task.truckId,
            licensePlate: task.licensePlate,
            driverId: task.driverId,
            driverName: task.driverName,
            driverPhone: task.driverPhone,
            billingCustomerId: task.billingCustomerId,
            billingCustomerName: task.billingCustomerName,
            billingCustomerCode: task.billingCustomerCode,
            sourceHubLinkedCustomerId: task.sourceHubLinkedCustomerId,
            destinationLinkedCustomerId: task.destinationLinkedCustomerId,
            isMultiDelivery: task.isMultiDelivery,
            deliveryStops: task.deliveryStops,
        };
        setDialogTaskType(task.taskType === "LINE_HAUL" ? "LINE_HAUL" : "FIRST_MILE");
        setDialogMode("create");
        setSelectedTask(seed);
        setIsDialogOpen(true);
    };

    const handleCancelTask = async (task: JobTask) => {
        if (!task.id) return;
        try {
            await updateDoc(doc(db, COLLECTIONS.TASKS, task.id), { status: "Cancelled", updatedAt: new Date() });
            try {
                const notify = httpsCallable(functions, "notifyTaskUpdate");
                await notify({ taskId: task.id, taskType: task.taskType, newDriverId: task.driverId || undefined, status: "Cancelled" });
            } catch (fcmErr) {
                console.warn("FCM notify after cancel:", fcmErr);
            }
            setCancelTask(null);
        } catch (err) {
            console.error("Failed to cancel task:", err);
        }
    };

    const filteredTasks = tasks.filter((task) => {
        if (isCustomer && customerScopeId) {
            const isSourceMatch = task.sourceHubLinkedCustomerId === customerScopeId;
            const isDestinationMatch = task.destinationLinkedCustomerId === customerScopeId;
            const isDeliveryStopMatch = task.deliveryStops?.some((stop) => stop.destinationLinkedCustomerId === customerScopeId) ?? false;
            if (!isSourceMatch && !isDestinationMatch && !isDeliveryStopMatch) return false;
        }
        if (selectedSOC !== "all" && normalizeSocIdToKey(task.destination || "") !== selectedSOC) return false;
        if (selectedHub !== "all" && task.sourceHub !== selectedHub) return false;
        return true;
    });

    return (
        <PagePermissionGuard capability={CAPABILITIES.operations_view_first_mile}>
            <div className="flex-1 space-y-4 p-8 pt-6">
                <div className="flex items-center justify-between space-y-2">
                    <div>
                        <h2 className="text-3xl font-bold tracking-tight">{t("jobAssign.title", "มอบหมายงาน")}</h2>
                        <p className="text-muted-foreground">{t("jobAssign.subtitle", "สร้างและมอบหมายงาน First Mile / Line Haul ให้คนขับ")}</p>
                    </div>
                    <div className="flex gap-3">
                        <Button variant="outline" size="icon" onClick={() => fetchHubs()} aria-label={t("firstMile.sources.refresh", "Refresh")}>
                            <RefreshCw className="h-4 w-4" />
                        </Button>
                        {/* Column chooser (Combolist) — pick which columns the table shows. */}
                        <DropdownMenu>
                            <DropdownMenuTrigger asChild>
                                <Button variant="outline" size="icon" aria-label={t("jobAssign.columns", "เลือกคอลัมน์")}>
                                    <Settings2 className="h-4 w-4" />
                                </Button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end" className="w-52">
                                <DropdownMenuLabel>{t("jobAssign.columns", "เลือกคอลัมน์")}</DropdownMenuLabel>
                                <DropdownMenuSeparator />
                                {JOB_COLUMNS.map((c) => (
                                    <DropdownMenuCheckboxItem
                                        key={c.key}
                                        checked={showCol(c.key)}
                                        onCheckedChange={() => toggleCol(c.key)}
                                        onSelect={(e) => e.preventDefault()}
                                    >
                                        {t(c.labelKey, c.fallback)}
                                    </DropdownMenuCheckboxItem>
                                ))}
                            </DropdownMenuContent>
                        </DropdownMenu>
                        {!isCustomer && (
                            <div className="flex gap-3">
                                <JobImportDialog onSuccess={() => {}} />
                                {/* One button — the FM/LH choice now lives inside the form itself. */}
                                <Button onClick={() => openCreate("FIRST_MILE")}>
                                    <Plus className="mr-2 h-4 w-4" />
                                    {t("jobAssign.addJob", "เพิ่มงาน")}
                                </Button>
                            </div>
                        )}
                    </div>
                </div>

                {/* Filters */}
                <Card>
                    <CardHeader className="pb-3">
                        <CardTitle className="text-lg">{t("firstMile.filters", "ตัวกรอง")}</CardTitle>
                    </CardHeader>
                    <CardContent className="flex flex-wrap gap-4">
                        <div className="flex flex-col gap-2 min-w-[160px]">
                            <label className="text-sm font-medium">{t("jobAssign.filter.type", "ประเภทงาน")}</label>
                            <Select value={taskTypeFilter} onValueChange={(v) => setTaskTypeFilter(v as TaskTypeFilter)}>
                                <SelectTrigger>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="all">{t("jobAssign.filter.allTypes", "ทั้งหมด")}</SelectItem>
                                    <SelectItem value="FIRST_MILE">First Mile</SelectItem>
                                    <SelectItem value="LINE_HAUL">Line Haul</SelectItem>
                                </SelectContent>
                            </Select>
                        </div>

                        <div className="flex flex-col gap-2">
                            <label className="text-sm font-medium">{t("firstMile.filter.date", "วันที่")}</label>
                            <Popover>
                                <PopoverTrigger asChild>
                                    <Button variant="outline" className={cn("w-[220px] justify-start text-left font-normal", !date && "text-muted-foreground")}>
                                        <CalendarIcon className="mr-2 h-4 w-4" />
                                        {date ? format(date, "dd/MM/yyyy") : <span>{t("firstMile.filter.pickDate", "เลือกวันที่")}</span>}
                                    </Button>
                                </PopoverTrigger>
                                <PopoverContent className="w-auto p-0" align="start">
                                    <Calendar mode="single" selected={date} onSelect={setDate} initialFocus />
                                </PopoverContent>
                            </Popover>
                        </div>

                        <div className="flex flex-col gap-2 min-w-[180px]">
                            <label className="text-sm font-medium">{t("jobAssign.filter.destination", "ปลายทาง (SOC)")}</label>
                            <Select value={selectedSOC} onValueChange={setSelectedSOC}>
                                <SelectTrigger>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="all">{t("jobAssign.filter.allDestinations", "ทุกปลายทาง")}</SelectItem>
                                    {SOC_KEYS.map((key) => (
                                        <SelectItem key={key} value={key}>{SOC_DESTINATIONS[key]}</SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>

                        <div className="flex flex-col gap-2 min-w-[180px] flex-1">
                            <label className="text-sm font-medium">{t("jobAssign.filter.source", "ต้นทาง")}</label>
                            <Select value={selectedHub} onValueChange={setSelectedHub}>
                                <SelectTrigger>
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="all">{t("jobAssign.filter.allSources", "ทุกต้นทาง")}</SelectItem>
                                    {hubs.slice(0, 50).map((hub, idx) => (
                                        <SelectItem key={idx} value={hub["Hub Code"] || `hub-${idx}`}>
                                            {hub["Hub Name Th"] || hub["Hub Name"] || hub["Hub Code"]}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        </div>
                    </CardContent>
                </Card>

                {/* Table */}
                <div className="border rounded-md bg-card shadow-sm overflow-hidden">
                    <Table>
                        <TableHeader className="bg-muted/50">
                            <TableRow>
                                {orderedVisibleCols.map((c) => (
                                    <TableHead key={c.key}>{t(c.labelKey, c.fallback)}</TableHead>
                                ))}
                                <TableHead className="text-right">{t("common.actions", "จัดการ")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {filteredTasks.length === 0 ? (
                                <TableRow>
                                    <TableCell colSpan={orderedVisibleCols.length + 1} className="h-24 text-center">
                                        {t("firstMile.table.noTasks", "ไม่มีงาน")}
                                    </TableCell>
                                </TableRow>
                            ) : (
                                filteredTasks.map((task) => (
                                    <TableRow key={task.id} className="hover:bg-muted/50 cursor-pointer" onClick={() => setDetailTask(task)}>
                                        {orderedVisibleCols.map((c) => (
                                            <TableCell
                                                key={c.key}
                                                className={cn(
                                                    c.key === "source" && "max-w-[200px]",
                                                    c.key === "actualPickup" && "text-sm whitespace-nowrap",
                                                )}
                                            >
                                                {renderCell(task, c.key)}
                                            </TableCell>
                                        ))}
                                        {!isCustomer && (
                                            <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                                                <DropdownMenu>
                                                    <DropdownMenuTrigger asChild>
                                                        <Button variant="ghost" size="icon" className="h-8 w-8">
                                                            <MoreHorizontal className="h-4 w-4" />
                                                        </Button>
                                                    </DropdownMenuTrigger>
                                                    <DropdownMenuContent align="end">
                                                        <DropdownMenuItem onClick={() => openEdit(task)}>{t("firstMile.table.edit", "แก้ไข")}</DropdownMenuItem>
                                                        <DropdownMenuItem onClick={() => openDuplicate(task)}>
                                                            <Copy className="mr-2 h-4 w-4" />
                                                            {t("jobAssign.duplicate", "ทำซ้ำเป็นงานใหม่")}
                                                        </DropdownMenuItem>
                                                        {task.status !== "Cancelled" && (
                                                            <DropdownMenuItem className="text-destructive focus:text-destructive" onClick={() => setCancelTask(task)}>
                                                                {t("firstMile.table.cancel", "ยกเลิก")}
                                                            </DropdownMenuItem>
                                                        )}
                                                    </DropdownMenuContent>
                                                </DropdownMenu>
                                            </TableCell>
                                        )}
                                    </TableRow>
                                ))
                            )}
                        </TableBody>
                    </Table>
                </div>

                {/* Create / edit dialog — ONE modal shell stays mounted; the FM/LH switch only swaps the form body
                    inside, so choosing a type no longer closes+reopens the modal (no double-modal flicker). */}
                <Dialog open={isDialogOpen} onOpenChange={setIsDialogOpen}>
                    <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
                        {dialogTaskType === "FIRST_MILE" ? (
                            <FirstMileTaskDialog embedded open={isDialogOpen} onOpenChange={setIsDialogOpen} mode={dialogMode} task={selectedTask} onSuccess={() => {}} taskType={dialogTaskType} onTaskTypeChange={handleDialogTaskTypeChange} />
                        ) : (
                            <LineHaulTaskDialog embedded open={isDialogOpen} onOpenChange={setIsDialogOpen} mode={dialogMode} task={selectedTask} onSuccess={() => {}} taskType={dialogTaskType} onTaskTypeChange={handleDialogTaskTypeChange} />
                        )}
                    </DialogContent>
                </Dialog>

                <Dialog open={!!cancelTask} onOpenChange={(open) => !open && setCancelTask(null)}>
                    <DialogContent>
                        <DialogHeader>
                            <DialogTitle>{t("firstMile.cancelTaskTitle", "ยกเลิกงาน")}</DialogTitle>
                            <DialogDescription>{t("firstMile.cancelTaskMessage", "ยืนยันการยกเลิกงานนี้?")}</DialogDescription>
                        </DialogHeader>
                        <DialogFooter>
                            <Button variant="outline" onClick={() => setCancelTask(null)}>{t("firstMile.cancelAbort", "ไม่")}</Button>
                            <Button variant="destructive" onClick={() => cancelTask && handleCancelTask(cancelTask)}>{t("firstMile.cancelConfirm", "ยืนยันยกเลิก")}</Button>
                        </DialogFooter>
                    </DialogContent>
                </Dialog>

                {/* Task detail modal */}
                <Dialog open={!!detailTask} onOpenChange={(open) => !open && setDetailTask(null)}>
                    <DialogContent className="max-w-lg max-h-[90vh] overflow-y-auto">
                        <DialogHeader>
                            <DialogTitle>{detailTask?.taskId ?? detailTask?.id}</DialogTitle>
                            <DialogDescription>{t("jobAssign.subtitle", "รายละเอียดงาน")}</DialogDescription>
                        </DialogHeader>
                        {detailTask && (
                            <div className="grid gap-4 py-2">
                                <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
                                    <span className="text-muted-foreground">{t("firstMile.table.date", "วันที่")}</span>
                                    <span>{detailTask.date ? format(detailTask.date, "dd/MM/yyyy") : "-"}</span>
                                    <span className="text-muted-foreground">{t("firstMile.table.time", "เวลา")}</span>
                                    <span>{detailTask.time ?? "-"}</span>
                                    <span className="text-muted-foreground">{t("firstMile.table.sourceHub", "ต้นทาง")}</span>
                                    <span className="font-medium">{routeLabel(detailTask.sourceHub)}</span>
                                    <span className="text-muted-foreground">{t("firstMile.table.destination", "ปลายทาง")}</span>
                                    <span>{routeLabel(detailTask.destination)}</span>
                                    <span className="text-muted-foreground">{t("firstMile.task.customer", "ลูกค้า")}</span>
                                    <span>{detailTask.billingCustomerName ?? detailTask.billingCustomerCode ?? "-"}</span>
                                    <span className="text-muted-foreground">{t("firstMile.task.actualPickupAt", "วันเวลารับงานจริง")}</span>
                                    <span>{detailTask.actualPickupAt ? format(detailTask.actualPickupAt as Date, "dd/MM/yyyy HH:mm") : "-"}</span>
                                    <span className="text-muted-foreground">{t("firstMile.table.type", "ประเภทรถ")}</span>
                                    <span>{detailTask.truckType ?? "-"}</span>
                                    <span className="text-muted-foreground">{t("firstMile.table.licensePlate", "ทะเบียน")}</span>
                                    <span className="font-mono">{detailTask.licensePlate ?? "-"}</span>
                                    <span className="text-muted-foreground">{t("firstMile.table.driver", "คนขับ")}</span>
                                    <span>{detailTask.driverName ?? "-"}</span>
                                    {detailTask.helperDriverIds && detailTask.helperDriverIds.length > 0 && (
                                        <>
                                            <span className="text-muted-foreground">{t("task.helpers", "ผู้ช่วย")}</span>
                                            <span>{detailTask.helperDriverIds.map((id) => helperNames[id] ?? id).join(", ")}</span>
                                        </>
                                    )}
                                    <span className="text-muted-foreground">{t("firstMile.table.status", "สถานะ")}</span>
                                    <span>
                                        <Badge variant={detailTask.status === "Cancelled" ? "secondary" : "outline"}>{detailTask.status}</Badge>
                                    </span>
                                </div>
                            </div>
                        )}
                        <DialogFooter>
                            {detailTrip && (
                                <Button variant="outline" onClick={() => setEditTripDialogOpen(true)} className="mr-auto">
                                    <Pencil className="mr-2 h-4 w-4" />
                                    {t("firstMile.task.editTrip", "แก้ไขเที่ยว")}
                                </Button>
                            )}
                            <Button variant="outline" onClick={() => setDetailTask(null)}>{t("firstMile.task.cancel", "ปิด")}</Button>
                            <Button onClick={() => { if (detailTask) { const t2 = detailTask; setDetailTask(null); openEdit(t2); } }}>
                                {detailTask?.status === "Cancelled" ? t("firstMile.table.assign", "มอบหมายใหม่") : t("firstMile.table.edit", "แก้ไข")}
                            </Button>
                        </DialogFooter>
                    </DialogContent>
                </Dialog>

                {detailTrip && (
                    <EditTripDetailsDialog
                        open={editTripDialogOpen}
                        onOpenChange={setEditTripDialogOpen}
                        trip={detailTrip}
                        getSourceDisplayName={routeLabel}
                        onSuccess={() => setEditTripDialogOpen(false)}
                    />
                )}
            </div>
        </PagePermissionGuard>
    );
}
