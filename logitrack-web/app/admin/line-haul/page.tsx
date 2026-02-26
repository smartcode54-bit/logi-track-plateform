"use client";

import { useState, useEffect, useMemo } from "react";
import { useRouter } from "next/navigation";
import { format } from "date-fns";
import { Calendar as CalendarIcon, Plus } from "lucide-react";
import { LineHaulImportDialog } from "./import-dialog";
import { LineHaulTaskDialog } from "./task-dialog";
import { useLanguage } from "@/context/language";



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
import { MoreHorizontal } from "lucide-react";

export default function LineHaulPage() {
    const { t } = useLanguage();
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

    // Fetch Hubs directly from Firestore
    const fetchHubs = async () => {
        try {
            const querySnapshot = await getDocs(collection(db, "hubs"));
            const hubList = querySnapshot.docs.map(doc => {
                const data = doc.data();
                return {
                    'Hub Code': data.source_id ?? data.hubId ?? data.hubCode,
                    'Hub Name': data.source_name_en ?? data.hubName,
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

    const getSOCColor = (soc: string) => {
        const key = normalizeSocIdToKey(soc);
        switch (key) {
            case "SOCE": return "bg-emerald-600 hover:bg-emerald-700 text-white";
            case "SOCN": return "bg-blue-600 hover:bg-blue-700 text-white";
            case "SOCW": return "bg-orange-600 hover:bg-orange-700 text-white";
            default: return "bg-slate-600 text-white";
        }
    };

    const getDestinationLabel = (dest: string | undefined) =>
        dest ? (SOC_DESTINATIONS[dest as keyof typeof SOC_DESTINATIONS] ?? dest) : "-";

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
        <div className="flex-1 space-y-4 p-8 pt-6">
            <div className="flex items-center justify-between space-y-2">
                <div>
                    <h2 className="text-3xl font-bold tracking-tight">Line Haul Tasks</h2>
                    <p className="text-muted-foreground">
                        Manage all line haul tasks and check-ins
                    </p>
                </div>
                <div className="flex gap-3">
                    <LineHaulImportDialog onSuccess={() => { }} />
                    <div className="flex gap-3">
                        <Button onClick={handleCreate}>
                            <Plus className="mr-2 h-4 w-4" />
                            New Assignment
                        </Button>
                    </div>
                </div>
            </div>

            {/* Filters */}
            <Card>
                <CardHeader className="pb-3">
                    <CardTitle className="text-lg">Filters</CardTitle>
                </CardHeader>
                <CardContent className="flex flex-wrap gap-4">
                    <div className="flex flex-col gap-2">
                        <label className="text-sm font-medium">Date</label>
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
                                    {date ? format(date, "dd/MM/yyyy") : <span>Pick a date</span>}
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
                        <label className="text-sm font-medium">Destination (Hub)</label>
                        <Select value={selectedSOC} onValueChange={setSelectedSOC}>
                            <SelectTrigger>
                                <SelectValue placeholder="All Destinations" />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">All Destinations</SelectItem>
                                {SOC_KEYS.map(key => (
                                    <SelectItem key={key} value={key}>{SOC_DESTINATIONS[key]}</SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>

                    <div className="flex flex-col gap-2 min-w-[200px] flex-1">
                        <label className="text-sm font-medium">Source (SOC)</label>
                        <Select value={selectedHub} onValueChange={setSelectedHub}>
                            <SelectTrigger>
                                <SelectValue placeholder="Select Source" />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">All Sources</SelectItem>
                                {/* Limit mapped hubs for performance if list is huge */}
                                {hubs.slice(0, 50).map((hub, idx) => (
                                    <SelectItem key={idx} value={hub['Hub Code'] || hub['Code'] || `hub-${idx}`}>
                                        {hub['Hub Name'] || hub['station_name_en'] || hub['Hub Code']}
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
                            <TableHead>Date</TableHead>
                            <TableHead>Source (SOC)</TableHead>
                            <TableHead>Destination (Hub)</TableHead>
                            <TableHead>Time</TableHead>
                            <TableHead>Type</TableHead>
                            <TableHead>Task ID</TableHead>
                            <TableHead>License Plate</TableHead>
                            <TableHead>Driver</TableHead>
                            <TableHead>Phone</TableHead>
                            <TableHead>Status</TableHead>
                            <TableHead>Check-in</TableHead>
                            <TableHead className="text-right">Actions</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {filteredTasks.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={12} className="h-24 text-center">
                                    No Line Haul Tasks Found
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
                                    <TableCell className="font-medium">{task.sourceHub}</TableCell>
                                    <TableCell>
                                        <Badge className={cn("font-normal border-0", getSOCColor(task.destination || ""))}>
                                            {getDestinationLabel(task.destination)}
                                        </Badge>
                                    </TableCell>
                                    <TableCell>{task.time}</TableCell>
                                    <TableCell>
                                        <span className={cn(
                                            "inline-flex items-center rounded-md px-2 py-1 text-xs font-medium ring-1 ring-inset",
                                            task.truckType === "4WH" && "bg-blue-50 text-blue-700 ring-blue-600/20",
                                            task.truckType === "4WJ" && "bg-cyan-50 text-cyan-700 ring-cyan-600/20",
                                            task.truckType === "6WH" && "bg-purple-50 text-purple-700 ring-purple-600/20",
                                            task.truckType === "10WH" && "bg-gray-50 text-gray-600 ring-gray-500/10",
                                            task.truckType === "18WH" && "bg-orange-50 text-orange-700 ring-orange-600/20",
                                            task.truckType === "PICKUP" && "bg-green-50 text-green-700 ring-green-600/20",
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
                                                    <a href={task.checkInPhotoUrl} target="_blank" rel="noopener noreferrer" className="text-primary underline text-xs" onClick={(e) => e.stopPropagation()}>View photo</a>
                                                )}
                                                {task.checkInLat != null && task.checkInLng != null && (
                                                    <span className="text-muted-foreground text-xs">{task.checkInLat.toFixed(4)}, {task.checkInLng.toFixed(4)}</span>
                                                )}
                                            </span>
                                        ) : (
                                            <span className="text-muted-foreground">-</span>
                                        )}
                                    </TableCell>
                                    <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                                        <DropdownMenu>
                                            <DropdownMenuTrigger asChild>
                                                <Button variant="ghost" size="icon" className="h-8 w-8">
                                                    <MoreHorizontal className="h-4 w-4" />
                                                </Button>
                                            </DropdownMenuTrigger>
                                            <DropdownMenuContent align="end">
                                                <DropdownMenuItem onClick={() => handleEdit(task)}>
                                                    Edit
                                                </DropdownMenuItem>
                                                <DropdownMenuItem onClick={() => handleEdit(task)}>
                                                    Assign
                                                </DropdownMenuItem>
                                                {task.status !== "Cancelled" && (
                                                    <DropdownMenuItem
                                                        className="text-destructive focus:text-destructive"
                                                        onClick={() => setCancelTask(task)}
                                                    >
                                                        Cancel
                                                    </DropdownMenuItem>
                                                )}
                                            </DropdownMenuContent>
                                        </DropdownMenu>
                                    </TableCell>
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
                        <DialogTitle>Cancel Task</DialogTitle>
                        <DialogDescription>Are you sure you want to cancel this task?</DialogDescription>
                    </DialogHeader>
                    <DialogFooter>
                        <Button variant="outline" onClick={() => setCancelTask(null)}>No</Button>
                        <Button variant="destructive" onClick={() => cancelTask && handleCancelTask(cancelTask)}>Yes, Cancel</Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            {/* Task detail modal */}
            <Dialog open={!!detailTask} onOpenChange={(open) => !open && setDetailTask(null)}>
                <DialogContent className="max-w-lg max-h-[90vh] overflow-y-auto">
                    <DialogHeader>
                        <DialogTitle>View Details – {detailTask?.taskId ?? detailTask?.id}</DialogTitle>
                        <DialogDescription>Manage Line Haul details</DialogDescription>
                    </DialogHeader>
                    {detailTask && (
                        <div className="grid gap-4 py-2">
                            <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
                                <span className="text-muted-foreground">Date</span>
                                <span>{detailTask.date ? format(detailTask.date, "dd/MM/yyyy") : "-"}</span>
                                <span className="text-muted-foreground">Time</span>
                                <span>{detailTask.time ?? "-"}</span>
                                <span className="text-muted-foreground">Source Hub</span>
                                <span className="font-medium">{detailTask.sourceHub ?? "-"}</span>
                                <span className="text-muted-foreground">Destination</span>
                                <span>{getDestinationLabel(detailTask.destination)}</span>
                                <span className="text-muted-foreground">Type</span>
                                <span>{detailTask.truckType ?? "-"}</span>
                                <span className="text-muted-foreground">Task ID</span>
                                <span className="font-mono text-xs">{detailTask.taskId ?? "-"}</span>
                                <span className="text-muted-foreground">License Plate</span>
                                <span className="font-mono">{detailTask.licensePlate ?? "-"}</span>
                                <span className="text-muted-foreground">Driver</span>
                                <span>{detailTask.driverName ?? "-"}</span>
                                <span className="text-muted-foreground">Phone</span>
                                <span>{detailTask.driverPhone ?? "-"}</span>
                                <span className="text-muted-foreground">Status</span>
                                <span>
                                    <Badge variant={detailTask.status === "Cancelled" ? "secondary" : "outline"}>
                                        {detailTask.status}
                                    </Badge>
                                </span>
                            </div>
                            {(detailTask.checkInAt || detailTask.checkInPhotoUrl) && (
                                <div className="border-t pt-3 space-y-2">
                                    <h4 className="text-sm font-medium text-muted-foreground">Check-in</h4>
                                    <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm">
                                        {detailTask.checkInAt && (
                                            <>
                                                <span className="text-muted-foreground">Time</span>
                                                <span>{detailTask.checkInAt instanceof Date ? format(detailTask.checkInAt, "dd/MM/yyyy HH:mm") : String(detailTask.checkInAt)}</span>
                                            </>
                                        )}
                                        {detailTask.checkInLat != null && (
                                            <>
                                                <span className="text-muted-foreground">Location</span>
                                                <span>{detailTask.checkInLat.toFixed(5)}, {detailTask.checkInLng?.toFixed(5)}</span>
                                            </>
                                        )}
                                    </div>
                                    {detailTask.checkInPhotoUrl && (
                                        <a href={detailTask.checkInPhotoUrl} target="_blank" rel="noopener noreferrer" className="text-primary text-sm underline">View photo</a>
                                    )}
                                </div>
                            )}
                        </div>
                    )}
                    <DialogFooter>
                        <Button variant="outline" onClick={() => setDetailTask(null)}>Cancel</Button>
                        <Button onClick={() => { if (detailTask) { setDetailTask(null); handleEdit(detailTask); } }}>
                            {detailTask?.status === "Cancelled" ? "Re-assign" : "Edit"}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
}
