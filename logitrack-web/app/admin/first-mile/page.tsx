"use client";

import { useState, useEffect, useMemo } from "react";
import { useRouter } from "next/navigation";
import { format } from "date-fns";
import { Calendar as CalendarIcon, Plus } from "lucide-react";
import { FirstMileImportDialog } from "./import-dialog";
import { FirstMileTaskDialog } from "./task-dialog";



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
import { SOC_DESTINATIONS, SOC_KEYS, FirstMileTask } from "@/validate/firstMileTaskSchema";
import { collection, onSnapshot, query, orderBy, limit } from "firebase/firestore";
import { db } from "@/firebase/client";

export default function FirstMilePage() {
    const [date, setDate] = useState<Date | undefined>(new Date());
    const [tasks, setTasks] = useState<FirstMileTask[]>([]);
    const [hubs, setHubs] = useState<Record<string, any>[]>([]);
    const [selectedHub, setSelectedHub] = useState<string>("all");
    const [selectedSOC, setSelectedSOC] = useState<string>("all");

    // Dialog State
    const [isDialogOpen, setIsDialogOpen] = useState(false);
    const [dialogMode, setDialogMode] = useState<"create" | "edit">("create");
    const [selectedTask, setSelectedTask] = useState<Partial<FirstMileTask> | undefined>(undefined);

    // Fetch Hubs
    const fetchHubs = async () => {
        try {
            const res = await fetch('/api/hubs');
            if (res.ok) {
                const data = await res.json();
                setHubs(data.hubs || []);
            }
        } catch (err) {
            console.error("Failed to fetch hubs", err);
        }
    };

    useEffect(() => {
        fetchHubs();
    }, []);

    // Listen to Tasks
    useEffect(() => {
        const q = query(collection(db, "first_mile_tasks"), orderBy("createdAt", "desc"), limit(100));
        const unsubscribe = onSnapshot(q, (snapshot) => {
            const fetched: FirstMileTask[] = snapshot.docs.map(doc => ({
                id: doc.id,
                ...doc.data(),
                date: doc.data().date?.toDate(), // Convert timestamp
                createdAt: doc.data().createdAt?.toDate(),
                updatedAt: doc.data().updatedAt?.toDate(),
            })) as FirstMileTask[];
            setTasks(fetched);
        });
        return () => unsubscribe();
    }, []);

    const getSOCColor = (soc: string) => {
        switch (soc) {
            case "SOC-E": return "bg-emerald-600 hover:bg-emerald-700 text-white";
            case "SOC-N": return "bg-blue-600 hover:bg-blue-700 text-white";
            case "SOC-W": return "bg-orange-600 hover:bg-orange-700 text-white";
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

    // Filter Logic
    const filteredTasks = tasks.filter(task => {
        // Date match (compare dd/MM/yyyy)
        if (date) {
            const filterStr = format(date, "dd/MM/yyyy");
            const taskStr = task.date ? format(task.date, "dd/MM/yyyy") : "";
            if (filterStr !== taskStr) return false;
        }
        // SOC
        if (selectedSOC !== "all" && task.destination !== selectedSOC) return false;
        // Hub
        if (selectedHub !== "all" && task.sourceHub !== selectedHub) return false;

        return true;
    });

    return (
        <div className="flex-1 space-y-4 p-8 pt-6">
            <div className="flex items-center justify-between space-y-2">
                <div>
                    <h2 className="text-3xl font-bold tracking-tight">First Mile Tasks</h2>
                    <p className="text-muted-foreground">
                        Assign drivers to pick up from Hubs and deliver to SOCs (First Step of Day Trip).
                    </p>
                </div>
                <div className="flex gap-3">
                    <div className="flex gap-3">
                        <FirstMileImportDialog onSuccess={() => { }} />
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
                        <label className="text-sm font-medium">Destination (SOC)</label>
                        <Select value={selectedSOC} onValueChange={setSelectedSOC}>
                            <SelectTrigger>
                                <SelectValue placeholder="All SOCs" />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">All SOCs</SelectItem>
                                {SOC_KEYS.map(key => (
                                    <SelectItem key={key} value={key}>{SOC_DESTINATIONS[key]}</SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>

                    <div className="flex flex-col gap-2 min-w-[200px] flex-1">
                        <label className="text-sm font-medium">Source Hub</label>
                        <Select value={selectedHub} onValueChange={setSelectedHub}>
                            <SelectTrigger>
                                <SelectValue placeholder="Select Hub..." />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">All Hubs</SelectItem>
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
                            <TableHead>Source Hub</TableHead>
                            <TableHead>Destination</TableHead>
                            <TableHead>Time</TableHead>
                            <TableHead>Type</TableHead>
                            <TableHead>Shipment ID</TableHead>
                            <TableHead>License Plate</TableHead>
                            <TableHead>Driver</TableHead>
                            <TableHead>Phone</TableHead>
                            <TableHead className="text-right">Actions</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {filteredTasks.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={10} className="h-24 text-center">
                                    No tasks found. Create one or import from Excel.
                                </TableCell>
                            </TableRow>
                        ) : (
                            filteredTasks.map((task) => (
                                <TableRow key={task.id} className="hover:bg-muted/50">
                                    <TableCell>{task.date ? format(task.date, 'dd/MM/yyyy') : '-'}</TableCell>
                                    <TableCell className="font-medium">{task.sourceHub}</TableCell>
                                    <TableCell>
                                        <Badge className={cn("font-normal border-0", getSOCColor(task.destination || ""))}>
                                            {task.destination ? SOC_DESTINATIONS[task.destination as keyof typeof SOC_DESTINATIONS] : task.destination}
                                        </Badge>
                                    </TableCell>
                                    <TableCell>{task.time}</TableCell>
                                    <TableCell>
                                        <span className={cn(
                                            "inline-flex items-center rounded-md px-2 py-1 text-xs font-medium ring-1 ring-inset",
                                            task.truckType === "4WH" && "bg-blue-50 text-blue-700 ring-blue-600/20",
                                            task.truckType === "4WJ" && "bg-cyan-50 text-cyan-700 ring-cyan-600/20",
                                            task.truckType === "6WH" && "bg-purple-50 text-purple-700 ring-purple-600/20",
                                            task.truckType === "10WH" && "bg-gray-50 text-gray-600 ring-gray-500/10"
                                        )}>
                                            {task.truckType}
                                        </span>
                                    </TableCell>
                                    <TableCell className="font-mono text-sm">{task.FirstMileTaskId}</TableCell>
                                    <TableCell className="font-mono">{task.licensePlate}</TableCell>
                                    <TableCell>{task.driverName}</TableCell>
                                    <TableCell className="text-sm text-muted-foreground">{task.driverPhone}</TableCell>
                                    <TableCell className="text-right">
                                        <Button
                                            variant="ghost"
                                            size="sm"
                                            onClick={() => handleEdit(task)}
                                        >
                                            Edit
                                        </Button>
                                    </TableCell>
                                </TableRow>
                            ))
                        )}
                    </TableBody>
                </Table>
            </div>

            <FirstMileTaskDialog
                open={isDialogOpen}
                onOpenChange={setIsDialogOpen}
                mode={dialogMode}
                task={selectedTask}
                onSuccess={() => { }} // Could show toast
            />
        </div>
    );
}
