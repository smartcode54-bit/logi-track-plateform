"use client";

import { useEffect, useState } from "react";
import {
    Truck,
    User,
    ClipboardList,
    ArrowRight,
    Search,
    Download,
    MoreHorizontal,
    Bell,
    Settings,
    Plus,
    LayoutDashboard
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";

import { Combobox } from "@/components/ui/combobox";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow
} from "@/components/ui/table";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger
} from "@/components/ui/dropdown-menu";
import { toast } from "sonner";

import {
    getActiveAssignments,
    getAvailableDrivers,
    getAvailableTrucks,
    createAssignment,
    terminateAssignment,
    getRecentHistory,
    type AssignmentData,
    type DriverOption,
    type TruckOption
} from "./actions.client";
import { useAuth } from "@/context/auth";

export default function TruckAssignmentPage() {
    const auth = useAuth();
    const currentUser = auth?.currentUser;
    const [stats, setStats] = useState({
        totalTrucks: 0,
        availableDrivers: 0,
        activeAssignments: 0
    });

    const [assignments, setAssignments] = useState<AssignmentData[]>([]);
    const [history, setHistory] = useState<AssignmentData[]>([]);
    const [drivers, setDrivers] = useState<DriverOption[]>([]);
    const [trucks, setTrucks] = useState<TruckOption[]>([]);

    const [selectedDriver, setSelectedDriver] = useState<string>("");
    const [selectedTruck, setSelectedTruck] = useState<string>("");
    const [isSubmitting, setIsSubmitting] = useState(false);
    const [isLoading, setIsLoading] = useState(true);

    const fetchData = async () => {
        setIsLoading(true);
        try {
            const [
                fetchedAssignments,
                fetchedHistory,
                fetchedDrivers,
                fetchedTrucks
            ] = await Promise.all([
                getActiveAssignments(),
                getRecentHistory(),
                getAvailableDrivers(),
                getAvailableTrucks()
            ]);

            setAssignments(fetchedAssignments);
            setHistory(fetchedHistory);

            // Filter out assigned drivers/trucks based on active assignments
            // (Simple client-side filter for now)
            const assignedDriverIds = new Set(fetchedAssignments.map(a => a.driverId));
            const assignedTruckIds = new Set(fetchedAssignments.map(a => a.truckId));

            const availableDrivers = fetchedDrivers.filter(d => !assignedDriverIds.has(d.id));
            // Do NOT filter out assigned trucks. Trucks can have multiple assignments now.
            // We might want to filter out 'maintenance' trucks, but that's handled in getAvailableTrucks.
            const availableTrucks = fetchedTrucks;

            setDrivers(availableDrivers);
            setTrucks(availableTrucks);

            setStats({
                totalTrucks: fetchedTrucks.length + assignedTruckIds.size, // Approximation
                availableDrivers: availableDrivers.length,
                activeAssignments: fetchedAssignments.length
            });

        } catch (error) {
            console.error("Failed to fetch data", error);
            toast.error("Failed to load dashboard data");
        } finally {
            setIsLoading(false);
        }
    };

    useEffect(() => {
        fetchData();
    }, []);

    const handleDeploy = async () => {
        if (!selectedDriver || !selectedTruck) {
            toast.error("Please select both a driver and a truck");
            return;
        }

        setIsSubmitting(true);
        try {
            const driver = drivers.find(d => d.id === selectedDriver);
            const truck = trucks.find(t => t.id === selectedTruck);

            if (!driver || !truck) throw new Error("Invalid selection");

            // TODO: QA Insight - Block Dispatch if Insurance is OVERDUE
            // Check truck.insuranceStatus or similar before proceeding
            // if (truck.insuranceStatus === 'overdue') throw new Error("Truck has overdue insurance");

            await createAssignment({
                truckId: truck.id,
                driverId: driver.id,
                driverName: driver.name,
                truckPlate: truck.plate,
                truckModel: truck.model,
                adminName: currentUser?.displayName || "Admin",
            });

            toast.success("Assignment deployed successfully");
            setSelectedDriver("");
            setSelectedTruck("");
            fetchData(); // Refresh data
        } catch (error) {
            console.error("Deployment failed", error);
            toast.error("Failed to deploy assignment");
        } finally {
            setIsSubmitting(false);
        }
    };

    const handleRevoke = async (assignmentId: string, truckId: string, driverId: string) => {
        if (!confirm("Are you sure you want to revoke this assignment/return the vehicle?")) return;

        try {
            await terminateAssignment(assignmentId, truckId, driverId);
            toast.success("Assignment revoked successfully");
            fetchData();
        } catch (error) {
            console.error("Revocation failed", error);
            toast.error("Failed to revoke assignment");
        }
    };

    return (
        <div className="min-h-screen bg-background text-foreground p-6 space-y-6">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">Manage Truck Assignments</h1>
                    <p className="text-muted-foreground mt-1">
                        Pair drivers with vehicles and monitor active fleet deployments.
                    </p>
                </div>
                <div className="flex gap-3">
                    <Button variant="outline" className="gap-2 bg-card border-border/50 text-foreground hover:bg-muted/50">
                        View Maps
                    </Button>
                    <Button className="bg-blue-600 hover:bg-blue-700 text-white gap-2 shadow-lg shadow-blue-900/20">
                        <Plus className="h-4 w-4" />
                        Quick Action
                    </Button>
                </div>
            </div>

            {/* Stats Cards */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                    <CardContent className="p-6">
                        <div className="flex justify-between items-start">
                            <div className="space-y-2">
                                <p className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Total Trucks</p>
                                <div className="flex items-baseline gap-2">
                                    <span className="text-3xl font-bold font-mono text-foreground">
                                        {isLoading ? "-" : stats.totalTrucks}
                                    </span>
                                    <span className="text-xs font-medium text-green-500">~+5%</span>
                                </div>
                            </div>
                            <div className="p-2 bg-blue-500/10 rounded-lg text-blue-500 group-hover:scale-110 transition-transform">
                                <Truck className="h-5 w-5" />
                            </div>
                        </div>
                    </CardContent>
                </Card>

                <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                    <CardContent className="p-6">
                        <div className="flex justify-between items-start">
                            <div className="space-y-2">
                                <p className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Available Drivers</p>
                                <div className="flex items-baseline gap-2">
                                    <span className="text-3xl font-bold font-mono text-foreground">
                                        {isLoading ? "-" : stats.availableDrivers}
                                    </span>
                                    <span className="text-xs font-medium text-orange-500">↘-2%</span>
                                </div>
                            </div>
                            <div className="p-2 bg-blue-500/10 rounded-lg text-blue-500 group-hover:scale-110 transition-transform">
                                <User className="h-5 w-5" />
                            </div>
                        </div>
                    </CardContent>
                </Card>

                <Card className="bg-card/50 border-border/50 shadow-sm relative overflow-hidden group">
                    <CardContent className="p-6">
                        <div className="flex justify-between items-start">
                            <div className="space-y-2">
                                <p className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Active Assignments</p>
                                <div className="flex items-baseline gap-2">
                                    <span className="text-3xl font-bold font-mono text-foreground">
                                        {isLoading ? "-" : stats.activeAssignments}
                                    </span>
                                    <span className="text-xs font-medium text-green-500">~+12%</span>
                                </div>
                            </div>
                            <div className="p-2 bg-blue-500/10 rounded-lg text-blue-500 group-hover:scale-110 transition-transform">
                                <ClipboardList className="h-5 w-5" />
                            </div>
                        </div>
                    </CardContent>
                </Card>
            </div>

            {/* New Assignment Section */}
            <div className="rounded-xl border border-border/50 bg-card overflow-hidden">
                <div className="p-6 border-b border-border/50">
                    <h2 className="text-lg font-semibold flex items-center gap-2">
                        <ArrowRight className="h-4 w-4 text-blue-500" />
                        New Assignment
                    </h2>
                </div>
                <div className="p-8">
                    <div className="flex flex-col lg:flex-row items-stretch gap-6">
                        {/* Driver Select */}
                        <div className="flex-1 space-y-4">
                            <label className="text-sm font-medium text-foreground">1. Select Available Driver</label>

                            <Combobox
                                options={drivers.map(d => ({
                                    value: d.id,
                                    label: `${d.name} ${d.licenseNumber ? `(${d.licenseNumber})` : ''}`
                                }))}
                                value={selectedDriver}
                                onSelect={setSelectedDriver}
                                placeholder="Search driver..."
                                searchPlaceholder="Search by name..."
                                emptyText="No available drivers found."
                                className="h-12 text-base bg-background/50 border-border/50"
                            />

                            <div className="h-24 rounded-lg border border-dashed border-border/60 flex items-center gap-4 px-4 bg-muted/20">
                                <div className="h-10 w-10 rounded-full bg-muted flex items-center justify-center text-muted-foreground">
                                    <User className="h-5 w-5" />
                                </div>
                                <span className="text-sm text-muted-foreground italic">
                                    {drivers.find(d => d.id === selectedDriver)?.name || "No driver selected"}
                                </span>
                            </div>
                        </div>

                        {/* Arrow */}
                        <div className="hidden lg:flex items-center justify-center pt-8">
                            <div className="h-10 w-10 rounded-full bg-blue-600/10 text-blue-500 flex items-center justify-center">
                                <ArrowRight className="h-5 w-5" />
                            </div>
                        </div>

                        {/* Truck Select */}
                        <div className="flex-1 space-y-4">
                            <label className="text-sm font-medium text-foreground">2. Select Available Truck</label>

                            <Combobox
                                options={trucks.map(t => ({
                                    value: t.id,
                                    label: `${t.model} (${t.plate})`
                                }))}
                                value={selectedTruck}
                                onSelect={setSelectedTruck}
                                placeholder="Search truck..."
                                searchPlaceholder="Search by plate or model..."
                                emptyText="No available trucks found."
                                className="h-12 text-base bg-background/50 border-border/50"
                            />

                            <div className="h-24 rounded-lg border border-dashed border-border/60 flex items-center gap-4 px-4 bg-muted/20">
                                <div className="h-10 w-10 rounded-full bg-muted flex items-center justify-center text-muted-foreground">
                                    <Truck className="h-5 w-5" />
                                </div>
                                <span className="text-sm text-muted-foreground italic">
                                    {trucks.find(t => t.id === selectedTruck)
                                        ? `${trucks.find(t => t.id === selectedTruck)?.model} (${trucks.find(t => t.id === selectedTruck)?.plate})`
                                        : "No vehicle selected"
                                    }
                                </span>
                            </div>
                        </div>
                    </div>

                    <div className="mt-8 flex flex-col items-center gap-4">
                        <Button
                            size="lg"
                            className="w-full max-w-sm bg-blue-600 hover:bg-blue-700 text-white shadow-lg shadow-blue-900/20 h-12 text-base font-semibold"
                            onClick={handleDeploy}
                            disabled={isSubmitting}
                        >
                            {isSubmitting ? "Deploying..." : "Confirm and Deploy Assignment"}
                        </Button>
                        <div className="flex items-center gap-2 text-xs text-muted-foreground">
                            <span className="relative flex h-2 w-2">
                                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-green-400 opacity-75"></span>
                                <span className="relative inline-flex rounded-full h-2 w-2 bg-green-500"></span>
                            </span>
                            Syncing live with Firebase database
                        </div>
                    </div>
                </div>
            </div>

            {/* History Table */}
            <div className="rounded-xl border border-border/50 bg-card overflow-hidden">
                <div className="p-6 border-b border-border/50 flex justify-between items-center">
                    <h2 className="text-lg font-semibold flex items-center gap-2">
                        <Settings className="h-4 w-4 text-blue-500" />
                        Assignment History
                    </h2>
                    <Button variant="ghost" size="sm" className="text-blue-500 hover:text-blue-600 hover:bg-blue-500/10">
                        Download CSV
                    </Button>
                </div>
                <Table>
                    <TableHeader className="bg-muted/30">
                        <TableRow className="hover:bg-transparent border-border/50">
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase pl-6">Timestamp</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Driver</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Vehicle</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Admin</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase">Status</TableHead>
                            <TableHead className="text-xs font-semibold tracking-wider text-muted-foreground uppercase text-right pr-6">Actions</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {isLoading ? (
                            <TableRow>
                                <TableCell colSpan={6} className="h-24 text-center text-muted-foreground">Loading history...</TableCell>
                            </TableRow>
                        ) : history.length === 0 ? (
                            <TableRow>
                                <TableCell colSpan={6} className="h-24 text-center text-muted-foreground">No recent assignments found.</TableCell>
                            </TableRow>
                        ) : (
                            history.map((item) => (
                                <TableRow key={item.id} className="border-border/50 hover:bg-muted/30 transition-colors">
                                    <TableCell className="pl-6 font-medium text-foreground/80 py-4">
                                        {/* Simple relative time logic for demo, usually use date-fns/moment */}
                                        {new Date(item.createdAt).toLocaleString()}
                                    </TableCell>
                                    <TableCell>
                                        <div className="flex items-center gap-3">
                                            <div className="h-7 w-7 rounded-full bg-blue-100 dark:bg-blue-900/30 text-blue-600 flex items-center justify-center text-xs font-bold ring-2 ring-background">
                                                {(item.driverName || 'UK').substring(0, 2).toUpperCase()}
                                            </div>
                                            {item.driverName}
                                        </div>
                                    </TableCell>
                                    <TableCell className="text-foreground/80">
                                        {item.truckModel} ({item.truckPlate})
                                    </TableCell>
                                    <TableCell className="text-muted-foreground">
                                        {item.adminName}
                                    </TableCell>
                                    <TableCell>
                                        {item.status === 'active' ? (
                                            <Badge className="bg-green-500/10 text-green-500 hover:bg-green-500/20 border-green-500/20 shadow-none uppercase text-[10px] tracking-wider px-2 py-0.5">
                                                Active
                                            </Badge>
                                        ) : (
                                            <Badge variant="secondary" className="bg-muted text-muted-foreground uppercase text-[10px] tracking-wider px-2 py-0.5">
                                                Revoked
                                            </Badge>
                                        )}
                                    </TableCell>
                                    <TableCell className="text-right pr-6">
                                        <DropdownMenu>
                                            <DropdownMenuTrigger asChild>
                                                <Button variant="ghost" size="icon" className="h-8 w-8 hover:bg-muted">
                                                    <MoreHorizontal className="h-4 w-4" />
                                                </Button>
                                            </DropdownMenuTrigger>
                                            <DropdownMenuContent align="end">
                                                <DropdownMenuItem>View Details</DropdownMenuItem>
                                                {item.status === 'active' && (
                                                    <DropdownMenuItem
                                                        className="text-destructive focus:text-destructive cursor-pointer"
                                                        onClick={() => handleRevoke(item.id, item.truckId, item.driverId)}
                                                    >
                                                        Revoke Assignment
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
        </div>
    );
}
