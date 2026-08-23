"use client";

import { useState, useEffect, useMemo, useCallback } from "react";
import { useRouter } from "next/navigation";
import { format } from "date-fns";
import { Calendar as CalendarIcon, Plus } from "lucide-react";
import { LineHaulImportDialog } from "./import-dialog";
import { LineHaulTaskDialog } from "./task-dialog";
import { useLanguage } from "@/context/language";
import { useCustomerScope } from "@/hooks/useCustomerScope";
import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";



import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import {
    Popover,
    PopoverContent,
    PopoverTrigger,
} from "@/components/ui/popover";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { SOC_DESTINATIONS, SOC_KEYS, Task as FirstMileTask, normalizeSocIdToKey } from "@/validate/taskSchema";
import { buildHubCodeToDisplayMapFromHubRows, resolveHubOrSocDisplay } from "@/lib/hubDisplay";
import { collection, getDocs, onSnapshot, query, orderBy, limit, doc, updateDoc, where } from "firebase/firestore";
import { httpsCallable } from "firebase/functions";
import { db, functions } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
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
import { MoreHorizontal, Pencil, RefreshCw } from "lucide-react";
import { EditTripDetailsDialog } from "@/app/app/driver-monitor/EditTripDetailsDialog";
import type { TripRecord } from "@/validate/tripRecordSchema";
import { useDriverNamesByAuthId } from "@/hooks/useDriverNamesByAuthId";

function toDate(val: unknown): Date | null {
    if (!val) return null;
    if (val instanceof Date) return val;
    if (typeof (val as { toDate?: () => Date })?.toDate === "function") return (val as { toDate: () => Date }).toDate();
    if (typeof val === "string") return new Date(val);
    return null;
}

export default function LineHaulPage() {
    const { t } = useLanguage();
    const { customerScopeId, isCustomer } = useCustomerScope();
    const [date, setDate] = useState<Date | undefined>(new Date());
    const [tasks, setTasks] = useState<FirstMileTask[]>([]);
    const [hubs, setHubs] = useState<Record<string, any>[]>([]);
    const [selectedHub, setSelectedHub] = useState<string>("all");
    const [selectedSOC, setSelectedSOC] = useState<string>("all");

    // Dialog State
    const [isDialogOpen, setIsDialogOpen] = useState(false);
    const [dialogMode, setDialogMode] = useState<"create" | "edit">("create");
    const [selectedTask, setSelectedTask] = useState<Partial<FirstMileTask> | undefined>(undefined);
    const [cancelTask, setCancelTask] = useState<FirstMileTask | null>(null);
    const [detailTask, setDetailTask] = useState<FirstMileTask | null>(null);
    const [detailTrip, setDetailTrip] = useState<TripRecord | null>(null);
    const [editTripDialogOpen, setEditTripDialogOpen] = useState(false);

    // Resolve helper Auth UIDs → names for the open task detail
    const helperNames = useDriverNamesByAuthId(detailTask?.helperDriverIds);

    // Fetch Hubs directly from Firestore
    const fetchHubs = async () => {
        try {
            const querySnapshot = await getDocs(collection(db, "hubs"));
            const hubList = querySnapshot.docs.map(doc => {
                const data = doc.data();
                return {
                    'Hub Code': data.source_id ?? data.hubId ?? data.hubCode,
                    'Hub Name': data.source_name_en ?? data.hubName,
                    'Hub Name Th':
                        (data.source_name_th ?? data.hubTHName ?? data.hub_th_name ?? data.station_name_th ?? "") ||
                        undefined,
                    // J&T hubs keep their real name on the linked customer — fallback for display (ADR 0019 follow-up)
                    linkedCustomerName: data.linkedCustomerName,
                    lat: data.latitude ?? data.lat,
                    lng: data.longitude ?? data.lng,
                    source: 'custom',
                    id: doc.id
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

    // When task detail opens, fetch linked trip_record (by taskId)
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
        const q = query(
            collection(db, COLLECTIONS.TRIP_RECORDS),
            where("taskId", "in", taskIds),
            limit(1)
        );
        getDocs(q).then((snap) => {
            const doc = snap.docs[0];
            if (!doc) {
                setDetailTrip(null);
                return;
            }
            const data = doc.data();
            setDetailTrip({
                id: doc.id,
                ...data,
                createdAt: toDate(data.createdAt) ?? undefined,
                updatedAt: toDate(data.updatedAt) ?? undefined,
                deliveredTimestamp: toDate(data.deliveredTimestamp) ?? undefined,
            } as TripRecord);
        }).catch(() => setDetailTrip(null));
    }, [detailTask?.id, detailTask?.taskId]);

    // Listen to Tasks
    useEffect(() => {
        const q = query(
            collection(db, COLLECTIONS.TASKS),
            where("taskType", "==", "LINE_HAUL"),
            orderBy("createdAt", "desc"),
            limit(100)
        );
        const unsubscribe = onSnapshot(q, (snapshot) => {
            const fetched: FirstMileTask[] = snapshot.docs.map(d => {
                const data = d.data();
                return {
                    id: d.id,
                    ...data,
                    date: data.date?.toDate?.() ?? data.date,
                    createdAt: data.createdAt?.toDate?.() ?? data.createdAt,
                    updatedAt: data.updatedAt?.toDate?.() ?? data.updatedAt,
                    checkInAt: data.checkInAt?.toDate?.() ?? data.checkInAt,
                };
            }) as FirstMileTask[];
            setTasks(fetched);
        });
        return () => unsubscribe();
    }, []);

    const hubDisplayMap = useMemo(() => buildHubCodeToDisplayMapFromHubRows(hubs), [hubs]);

    const routeLabel = useCallback(
        (code: string | null | undefined) => resolveHubOrSocDisplay(code ?? null, hubDisplayMap),
        [hubDisplayMap]
    );

    const getSOCColor = (soc: string) => {
        const key = normalizeSocIdToKey(soc);
        switch (key) {
            case "SOCE": return "bg-emerald-600 hover:bg-emerald-700 text-white";
            case "SOCN": return "bg-blue-600 hover:bg-blue-700 text-white";
            case "SOCW": return "bg-orange-600 hover:bg-orange-700 text-white";
            default: return "bg-slate-600 text-white";
        }
    };

    const handleCreate = () => {
        setDialogMode("create");
        setSelectedTask(undefined);
        setIsDialogOpen(true);
    };

    const handleEdit = (task: FirstMileTask) => {
        setDialogMode("edit");
        setSelectedTask(task);
        setIsDialogOpen(true);
    };

    const handleCancelTask = async (task: FirstMileTask) => {
        if (!task.id) return;
        try {
            await updateDoc(doc(db, COLLECTIONS.TASKS, task.id), {
                status: "Cancelled",
                updatedAt: new Date(),
            });
            try {
                const notify = httpsCallable(functions, "notifyTaskUpdate");
                await notify({ taskId: task.id, taskType: "LINE_HAUL", newDriverId: task.driverId || undefined, status: "Cancelled" });
            } catch (fcmErr) {
                console.warn("FCM notify after cancel:", fcmErr);
            }
            setCancelTask(null);
        } catch (err) {
            console.error("Failed to cancel task:", err);
        }
    };

    // Filter Logic
    const filteredTasks = tasks.filter(task => {
        // Customer scope: if customer role, only show tasks linked to their customer
        if (isCustomer && customerScopeId) {
            const isSourceMatch = task.sourceHubLinkedCustomerId === customerScopeId;
            const isDestinationMatch = task.destinationLinkedCustomerId === customerScopeId;
            const isDeliveryStopMatch = task.deliveryStops?.some(
                (stop) => stop.destinationLinkedCustomerId === customerScopeId
            ) ?? false;
            if (!isSourceMatch && !isDestinationMatch && !isDeliveryStopMatch) return false;
        }

        // Date match (compare dd/MM/yyyy)
        if (date) {
            const filterStr = format(date, "dd/MM/yyyy");
            const taskStr = task.date ? format(task.date, "dd/MM/yyyy") : "";
            if (filterStr !== taskStr) return false;
        }
        // SOC (compare normalized key so legacy "SOCE" and hub source_id both match)
        if (selectedSOC !== "all" && normalizeSocIdToKey(task.destination || "") !== selectedSOC) return false;
        // Hub
        if (selectedHub !== "all" && task.sourceHub !== selectedHub) return false;

        return true;
    });

    return (
        <PagePermissionGuard capability={CAPABILITIES.operations_view_line_haul}>
            <div className="flex-1 space-y-4 p-8 pt-6">
            <div className="flex items-center justify-between space-y-2">
                <div>
                    <h2 className="text-3xl font-bold tracking-tight">{t("lineHaul.title")}</h2>
                    <p className="text-muted-foreground">
                        {t("lineHaul.subtitle")}
                    </p>
                </div>
                <div className="flex gap-3">
                    <Button variant="outline" size="icon" onClick={() => fetchHubs()} aria-label="Refresh">
                        <RefreshCw className="h-4 w-4" />
                    </Button>
                    {!isCustomer && (
                        <>
                            <LineHaulImportDialog onSuccess={() => { }} />
                            <div className="flex gap-3">
                                <Button onClick={handleCreate}>
                                    <Plus className="mr-2 h-4 w-4" />
                                    {t("lineHaul.newAssignment")}
                                </Button>
                            </div>
                        </>
                    )}
                </div>
            </div>

            {/* Filters */}
            <Card>
                <CardHeader className="pb-3">
                    <CardTitle className="text-lg">{t("lineHaul.filters.title")}</CardTitle>
                </CardHeader>
                <CardContent className="flex flex-wrap gap-4">
                    <div className="flex flex-col gap-2">
                        <label className="text-sm font-medium">{t("lineHaul.filters.date")}</label>
                        <Popover>
                            <PopoverTrigger asChild>
                                <Button
                                    variant={"outline"}
                                    className={cn(
                                        "w-[240px] justify-start text-left font-normal",
                                        !date && "text-muted-foreground"
                                    )}
                                >
                                    <CalendarIcon className="mr-2 h-4 w-4" />
                                    {date ? format(date, "dd/MM/yyyy") : t("lineHaul.filters.pickDate")}
                                </Button>
                            </PopoverTrigger>
                            <PopoverContent className="w-auto p-0" align="start">
                                <Calendar
                                    mode="single"
                                    selected={date}
                                    onSelect={setDate}
                                    initialFocus
                                />
                            </PopoverContent>
                        </Popover>
                    </div>

                    <div className="flex flex-col gap-2 min-w-[200px]">
                        <label className="text-sm font-medium">{t("lineHaul.filters.destinationHub")}</label>
                        <Select value={selectedSOC} onValueChange={setSelectedSOC}>
                            <SelectTrigger>
                                <SelectValue placeholder={t("lineHaul.filters.allDestinations")} />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("lineHaul.filters.allDestinations")}</SelectItem>
                                {SOC_KEYS.map(key => (
                                    <SelectItem key={key} value={key}>{SOC_DESTINATIONS[key]}</SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>

                    <div className="flex flex-col gap-2 min-w-[200px] flex-1">
                        <label className="text-sm font-medium">{t("lineHaul.filters.sourceSOC")}</label>
                        <Select value={selectedHub} onValueChange={setSelectedHub}>
                            <SelectTrigger>
                                <SelectValue placeholder={t("lineHaul.filters.selectSource")} />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("lineHaul.filters.allSources")}</SelectItem>
                                {/* Limit mapped hubs for performance if list is huge */}
                                {hubs.slice(0, 50).map((hub, idx) => (
                                    <SelectItem key={idx} value={hub['Hub Code'] || hub['Code'] || `hub-${idx}`}>
                                        {hub['Hub Name Th'] || hub['Hub Name'] || hub['station_name_en'] || hub['Hub Code']}
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
                            <TableHead>{t("lineHaul.table.date")}</TableHead>
                            <TableHead>{t("lineHaul.table.source")}</TableHead>
                            <TableHead>{t("lineHaul.table.destination")}</TableHead>
                            <TableHead>{t("lineHaul.table.time")}</TableHead>
                            <TableHead>{t("lineHaul.table.type")}</TableHead>
                            <TableHead>{t("lineHaul.table.taskId")}</TableHead>
                            <TableHead>{t("lineHaul.table.licensePlate")}</TableHead>
                            <TableHead>{t("lineHaul.table.driver")}</TableHead>
                            <TableHead>{t("lineHaul.table.phone")}</TableHead>
                            <TableHead>{t("lineHaul.table.status")}</TableHead>
                            <TableHead>{t("lineHaul.table.checkIn")}</TableHead>
                            <TableHead className="text-right">{t("common.actions")}</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {filteredTasks.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={12} className="h-24 text-center">
                                    {t("lineHaul.noTasks")}
                                </TableCell>
                            </TableRow>
                        ) : (
                            filteredTasks.map((task) => (
                                <TableRow
                                    key={task.id}
                                    className="hover:bg-muted/50 cursor-pointer"
                                    onClick={() => setDetailTask(task)}
                                >
                                    <TableCell>{task.date ? format(task.date, 'dd/MM/yyyy') : '-'}</TableCell>
                                    <TableCell className="max-w-[220px]">
                                        <span className="font-medium leading-tight block">
                                            {routeLabel(task.sourceHub)}
                                        </span>
                                    </TableCell>
                                    <TableCell>
                                        <Badge className={cn("font-normal border-0", getSOCColor(task.destination || ""))}>
                                            {routeLabel(task.destination)}
                                        </Badge>
                                    </TableCell>
                                    <TableCell>{task.time}</TableCell>
                                    <TableCell>
                                        <span className={cn(
                                            "inline-flex items-center rounded-md px-2 py-1 text-xs font-medium ring-1 ring-inset",
                                            task.truckType === "4WJ" && "bg-cyan-50 text-cyan-700 ring-cyan-600/20",
                                            task.truckType === "6WH" && "bg-purple-50 text-purple-700 ring-purple-600/20",
                                            task.truckType === "10WH" && "bg-gray-50 text-gray-600 ring-gray-500/10",
                                            task.truckType === "18WH" && "bg-orange-50 text-orange-700 ring-orange-600/20",
                                            task.truckType === "4W" && "bg-green-50 text-green-700 ring-green-600/20",
                                            task.truckType === "VAN" && "bg-indigo-50 text-indigo-700 ring-indigo-600/20"
                                        )}>
                                            {task.truckType}
                                        </span>
                                    </TableCell>
                                    <TableCell className="font-mono text-sm">{task.taskId}</TableCell>
                                    <TableCell className="font-mono">{task.licensePlate}</TableCell>
                                    <TableCell>{task.driverName}</TableCell>
                                    <TableCell className="text-sm text-muted-foreground">{task.driverPhone}</TableCell>
                                    <TableCell>
                                        <Badge variant={task.status === "Cancelled" ? "secondary" : task.status === "Checked in" || task.status === "Completed" ? "default" : "outline"}>
                                            {task.status}
                                        </Badge>
                                    </TableCell>
                                    <TableCell className="text-sm">
                                        {task.checkInAt ? (
                                            <span className="flex flex-col gap-0.5">
                                                <span>{task.checkInAt instanceof Date ? format(task.checkInAt, "dd/MM/yy HH:mm") : "-"}</span>
                                                {task.checkInPhotoUrl && (
                                                    <a href={task.checkInPhotoUrl} target="_blank" rel="noopener noreferrer" className="text-primary underline text-xs" onClick={(e) => e.stopPropagation()}>{t("lineHaul.viewPhoto")}</a>
                                                )}
                                                {task.checkInLat != null && task.checkInLng != null && (
                                                    <span className="text-muted-foreground text-xs">{task.checkInLat.toFixed(4)}, {task.checkInLng.toFixed(4)}</span>
                                                )}
                                            </span>
                                        ) : (
                                            <span className="text-muted-foreground">-</span>
                                        )}
                                    </TableCell>
                                    {!isCustomer && (
                                        <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                                            <DropdownMenu>
                                                <DropdownMenuTrigger asChild>
                                                    <Button variant="ghost" size="icon" className="h-8 w-8">
                                                        <MoreHorizontal className="h-4 w-4" />
                                                    </Button>
                                                </DropdownMenuTrigger>
                                                <DropdownMenuContent align="end">
                                                    <DropdownMenuItem onClick={() => handleEdit(task)}>
                                                        {t("lineHaul.actions.edit")}
                                                    </DropdownMenuItem>
                                                    <DropdownMenuItem onClick={() => handleEdit(task)}>
                                                        {t("lineHaul.actions.assign")}
                                                    </DropdownMenuItem>
                                                    {task.status !== "Cancelled" && (
                                                        <DropdownMenuItem
                                                            className="text-destructive focus:text-destructive"
                                                            onClick={() => setCancelTask(task)}
                                                        >
                                                            {t("lineHaul.actions.cancel")}
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

            <LineHaulTaskDialog
                open={isDialogOpen}
                onOpenChange={setIsDialogOpen}
                mode={dialogMode}
                task={selectedTask}
                onSuccess={() => { }}
            />

            <Dialog open={!!cancelTask} onOpenChange={(open) => !open && setCancelTask(null)}>
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>{t("lineHaul.cancelTask.title")}</DialogTitle>
                        <DialogDescription>{t("lineHaul.cancelTask.confirm")}</DialogDescription>
                    </DialogHeader>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => setCancelTask(null)}>{t("lineHaul.cancelTask.no")}</Button>
                        <Button variant="destructive" onClick={() => cancelTask && handleCancelTask(cancelTask)}>{t("lineHaul.cancelTask.yesCancel")}</Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            {/* Task detail modal */}
            <Dialog open={!!detailTask} onOpenChange={(open) => !open && setDetailTask(null)}>
                <DialogContent className="max-w-lg max-h-[90vh] overflow-y-auto">
                    <DialogHeader>
                        <DialogTitle>{t("lineHaul.detail.title")} – {detailTask?.taskId ?? detailTask?.id}</DialogTitle>
                        <DialogDescription>{t("lineHaul.detail.manageDetails")}</DialogDescription>
                    </DialogHeader>
                    {detailTask && (
                        <div className="grid gap-4 py-2">
                            <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
                                <span className="text-muted-foreground">{t("lineHaul.table.date")}</span>
                                <span>{detailTask.date ? format(detailTask.date, "dd/MM/yyyy") : "-"}</span>
                                <span className="text-muted-foreground">{t("lineHaul.table.time")}</span>
                                <span>{detailTask.time ?? "-"}</span>
                                <span className="text-muted-foreground">{t("lineHaul.detail.sourceHub")}</span>
                                <span className="font-medium">{routeLabel(detailTask.sourceHub)}</span>
                                <span className="text-muted-foreground">{t("lineHaul.detail.destination")}</span>
                                <span>{routeLabel(detailTask.destination)}</span>
                                <span className="text-muted-foreground">{t("lineHaul.table.type")}</span>
                                <span>{detailTask.truckType ?? "-"}</span>
                                <span className="text-muted-foreground">{t("lineHaul.table.taskId")}</span>
                                <span className="font-mono text-xs">{detailTask.taskId ?? "-"}</span>
                                <span className="text-muted-foreground">{t("lineHaul.table.licensePlate")}</span>
                                <span className="font-mono">{detailTask.licensePlate ?? "-"}</span>
                                <span className="text-muted-foreground">{t("lineHaul.table.driver")}</span>
                                <span>{detailTask.driverName ?? "-"}</span>
                                <span className="text-muted-foreground">{t("lineHaul.table.phone")}</span>
                                <span>{detailTask.driverPhone ?? "-"}</span>
                                {detailTask.helperDriverIds && detailTask.helperDriverIds.length > 0 && (
                                    <>
                                        <span className="text-muted-foreground">{t("task.helpers", "Helpers")}</span>
                                        <span>{detailTask.helperDriverIds.map((id) => helperNames[id] ?? id).join(", ")}</span>
                                    </>
                                )}
                                <span className="text-muted-foreground">{t("lineHaul.table.status")}</span>
                                <span>
                                    <Badge variant={detailTask.status === "Cancelled" ? "secondary" : "outline"}>
                                        {detailTask.status}
                                    </Badge>
                                </span>
                            </div>
                            {(detailTask.checkInAt || detailTask.checkInPhotoUrl) && (
                                <div className="border-t pt-3 space-y-2">
                                    <h4 className="text-sm font-medium text-muted-foreground">{t("lineHaul.detail.checkIn")}</h4>
                                    <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
                                        {detailTask.checkInAt && (
                                            <>
                                                <span className="text-muted-foreground">{t("lineHaul.table.time")}</span>
                                                <span>{detailTask.checkInAt instanceof Date ? format(detailTask.checkInAt, "dd/MM/yyyy HH:mm") : String(detailTask.checkInAt)}</span>
                                            </>
                                        )}
                                        {detailTask.checkInLat != null && (
                                            <>
                                                <span className="text-muted-foreground">{t("lineHaul.detail.location")}</span>
                                                <span>{detailTask.checkInLat.toFixed(5)}, {detailTask.checkInLng?.toFixed(5)}</span>
                                            </>
                                        )}
                                    </div>
                                    {detailTask.checkInPhotoUrl && (
                                        <a href={detailTask.checkInPhotoUrl} target="_blank" rel="noopener noreferrer" className="text-primary text-sm underline">{t("lineHaul.viewPhoto")}</a>
                                    )}
                                </div>
                            )}
                        </div>
                    )}
                    <DialogFooter>
                        {detailTrip && (
                            <Button
                                variant="outline"
                                onClick={() => setEditTripDialogOpen(true)}
                                className="mr-auto"
                            >
                                <Pencil className="mr-2 h-4 w-4" />
                                {t("lineHaul.detail.editTrip")}
                            </Button>
                        )}
                        <Button variant="outline" onClick={() => setDetailTask(null)}>{t("leaveRequests.dialog.cancelButton")}</Button>
                        <Button onClick={() => { if (detailTask) { setDetailTask(null); handleEdit(detailTask); } }}>
                            {detailTask?.status === "Cancelled" ? t("lineHaul.detail.reassign") : t("lineHaul.actions.edit")}
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
