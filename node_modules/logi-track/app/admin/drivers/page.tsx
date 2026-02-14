"use client";

import { useState, useEffect, useMemo } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { collection, query, orderBy, onSnapshot, limit } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { Driver, DRIVER_STATUS_ENUM } from "@/validate/driverSchema";
import {
    Download,
    Loader2,
    Users,
    Activity,
    CalendarOff,
    MoreHorizontal,
    Plus
} from "lucide-react";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar";


export default function DriversListPage() {
    const router = useRouter();
    const [drivers, setDrivers] = useState<Driver[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);

    // Filters
    const [searchQuery, setSearchQuery] = useState("");
    const [statusFilter, setStatusFilter] = useState<string>("all");

    // Pagination
    const [currentPage, setCurrentPage] = useState(1);
    const itemsPerPage = 10;

    // Fetch drivers
    useEffect(() => {
        setLoading(true);
        const driversRef = collection(db, COLLECTIONS.DRIVERS);
        const q = query(driversRef, orderBy("createdAt", "desc"), limit(100));

        const unsubscribe = onSnapshot(q, (snapshot) => {
            const fetchedDrivers: Driver[] = [];
            snapshot.forEach((doc) => {
                const data = doc.data();
                // Safe handling of timestamps
                const createdAt = data.createdAt?.toDate ? data.createdAt.toDate() : new Date();

                fetchedDrivers.push({
                    id: doc.id,
                    firstName: data.firstName || "",
                    lastName: data.lastName || "",
                    mobile: data.mobile || "",
                    email: data.email || "",
                    status: data.status || "Active",
                    employmentType: data.employmentType || "FULL_TIME",
                    truckLicenseId: data.truckLicenseId || "",
                    // Assignment - Map directly from document
                    currentAssignment: data.currentAssignment,
                    ...data,
                    createdAt
                } as Driver);
            });
            setDrivers(fetchedDrivers);
            setLoading(false);
        }, (err) => {
            console.error("Error fetching drivers:", err);
            setError("Failed to load drivers");
            setLoading(false);
        });

        return () => unsubscribe();
    }, []);

    // Derived Stats
    const stats = useMemo(() => {
        const total = drivers.length;
        const active = drivers.filter(d => d.status === "Active" || d.status === "On-Duty").length;
        const onLeave = drivers.filter(d => d.status === "Inactive").length; // Mapping Inactive for now to "Out" bucket or similar
        return { total, active, onLeave };
    }, [drivers]);

    // Filtering Logic
    const filteredDrivers = useMemo(() => {
        return drivers.filter(driver => {
            const fullName = `${driver.firstName} ${driver.lastName}`.toLowerCase();
            const matchSearch =
                fullName.includes(searchQuery.toLowerCase()) ||
                driver.truckLicenseId?.toLowerCase().includes(searchQuery.toLowerCase()) ||
                driver.mobile?.toLowerCase().includes(searchQuery.toLowerCase());

            const matchStatus = statusFilter === "all" || driver.status === statusFilter;

            return matchSearch && matchStatus;
        });
    }, [drivers, searchQuery, statusFilter]);

    // Pagination Logic
    const paginatedDrivers = filteredDrivers.slice(
        (currentPage - 1) * itemsPerPage,
        currentPage * itemsPerPage
    );
    const totalPages = Math.ceil(filteredDrivers.length / itemsPerPage);

    const getStatusColor = (status: string) => {
        switch (status) {
            case "Active": return "bg-green-500/10 text-green-500 border-green-500/20";
            case "On-Duty": return "bg-blue-500/10 text-blue-500 border-blue-500/20";
            case "Inactive": return "bg-gray-500/10 text-gray-500 border-gray-500/20";
            default: return "bg-gray-500/10 text-gray-500";
        }
    };

    return (
        <div className="container mx-auto p-6 space-y-6 max-w-[1600px]">
            {/* Header */}
            <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">Driver Management</h1>
                    <p className="text-muted-foreground mt-1">
                        Manage and track your fleet's personnel efficiently in real-time.
                    </p>
                </div>
                <div className="flex gap-3">
                    <Button variant="outline" className="gap-2">
                        <Download className="h-4 w-4" />
                        Export
                    </Button>
                    <Button asChild>
                        <Link href="/admin/drivers/new">
                            <Plus className="mr-2 h-4 w-4" />
                            Add New Driver
                        </Link>
                    </Button>
                </div>
            </div>

            {/* Stats Cards */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Card className="bg-card">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">Total Drivers</p>
                            <div className="flex items-baseline gap-2 mt-1">
                                <h2 className="text-3xl font-bold">{stats.total}</h2>
                            </div>
                        </div>
                        <div className="h-12 w-12 rounded-full bg-slate-100 dark:bg-slate-800 flex items-center justify-center text-slate-500">
                            <Users className="h-6 w-6" />
                        </div>
                    </CardContent>
                </Card>
                <Card className="bg-card">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">Active / On-Duty</p>
                            <div className="flex items-baseline gap-2 mt-1">
                                <h2 className="text-3xl font-bold">{stats.active}</h2>
                            </div>
                        </div>
                        <div className="h-12 w-12 rounded-full bg-blue-100 dark:bg-blue-900/30 flex items-center justify-center text-blue-500">
                            <Activity className="h-6 w-6" />
                        </div>
                    </CardContent>
                </Card>
                <Card className="bg-card">
                    <CardContent className="p-6 flex items-center justify-between">
                        <div>
                            <p className="text-sm font-medium text-muted-foreground">Inactive</p>
                            <div className="flex items-baseline gap-2 mt-1">
                                <h2 className="text-3xl font-bold">{stats.onLeave}</h2>
                            </div>
                        </div>
                        <div className="h-12 w-12 rounded-full bg-gray-100 dark:bg-gray-800 flex items-center justify-center text-gray-500">
                            <CalendarOff className="h-6 w-6" />
                        </div>
                    </CardContent>
                </Card>
            </div>

            {/* Main Content & Filters */}
            <div className="space-y-4">
                <div className="flex flex-col sm:flex-row gap-4 bg-card/50 p-4 rounded-lg border border-border/50">
                    <div className="relative flex-1">
                        <Users className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                        <Input
                            placeholder="Search by Name, License ID, or Mobile..."
                            className="pl-10 bg-background/50 border-border/50"
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                        />
                    </div>
                    <div className="flex gap-2 overflow-x-auto pb-2 sm:pb-0">
                        <Button
                            variant={statusFilter === "all" ? "secondary" : "ghost"}
                            onClick={() => setStatusFilter("all")}
                            className="whitespace-nowrap"
                        >
                            All
                        </Button>
                        <Button
                            variant={statusFilter === "On-Duty" ? "secondary" : "ghost"}
                            onClick={() => setStatusFilter("On-Duty")}
                            className="whitespace-nowrap"
                        >
                            On-Duty
                        </Button>
                        <Button
                            variant={statusFilter === "Active" ? "secondary" : "ghost"}
                            onClick={() => setStatusFilter("Active")}
                            className="whitespace-nowrap"
                        >
                            Active
                        </Button>
                        <Button
                            variant={statusFilter === "Inactive" ? "secondary" : "ghost"}
                            onClick={() => setStatusFilter("Inactive")}
                            className="whitespace-nowrap"
                        >
                            Inactive
                        </Button>
                    </div>
                </div>

                <div className="border rounded-lg bg-card overflow-hidden">
                    <Table>
                        <TableHeader className="bg-muted/50">
                            <TableRow className="border-b border-border/50">
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">Driver</TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">Contact</TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">Employment</TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">Assigned Vehicle</TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">License ID</TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">License Type</TableHead>
                                <TableHead className="uppercase text-xs font-semibold text-muted-foreground tracking-wider">Status</TableHead>
                                <TableHead className="text-right uppercase text-xs font-semibold text-muted-foreground tracking-wider">Actions</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {loading ? (
                                <TableRow>
                                    <TableCell colSpan={6} className="h-32 text-center">
                                        <div className="flex flex-col items-center justify-center gap-2">
                                            <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
                                            <p className="text-sm text-muted-foreground">Loading drivers...</p>
                                        </div>
                                    </TableCell>
                                </TableRow>
                            ) : paginatedDrivers.length === 0 ? (
                                <TableRow>
                                    <TableCell colSpan={6} className="h-32 text-center">
                                        <p className="text-sm text-muted-foreground">No drivers found.</p>
                                    </TableCell>
                                </TableRow>
                            ) : (
                                paginatedDrivers.map((driver) => (
                                    <TableRow key={driver.id} className="cursor-pointer hover:bg-muted/50 transition-colors border-b border-border/50" onClick={() => router.push(`/admin/drivers/view?id=${driver.id}`)}>
                                        <TableCell>
                                            <div className="flex items-center gap-3">
                                                <Avatar className="h-9 w-9 border border-border">
                                                    <AvatarImage src={driver.profileImage} alt={driver.firstName} />
                                                    <AvatarFallback>{driver.firstName.slice(0, 1).toUpperCase()}{driver.lastName.slice(0, 1).toUpperCase()}</AvatarFallback>
                                                </Avatar>
                                                <div className="flex flex-col">
                                                    <span className="font-medium text-sm text-foreground">{driver.firstName} {driver.lastName}</span>
                                                    <span className="text-xs text-muted-foreground">{driver.email}</span>
                                                </div>
                                            </div>
                                        </TableCell>
                                        <TableCell className="text-sm text-muted-foreground">{driver.mobile}</TableCell>
                                        <TableCell>
                                            <div className="flex flex-col">
                                                <span className="text-sm font-medium">{driver.employmentType.replace("_", " ")}</span>
                                                {driver.employmentType === 'SUBCONTRACTOR' && driver.subcontractorName && (
                                                    <span className="text-xs text-muted-foreground">{driver.subcontractorName}</span>
                                                )}
                                            </div>
                                        </TableCell>
                                        <TableCell>
                                            {driver.currentAssignment ? (
                                                <div className="flex flex-col">
                                                    <span className="font-medium text-sm text-blue-600">{driver.currentAssignment.truckPlate}</span>
                                                    <span className="text-xs text-muted-foreground">{(driver.currentAssignment as any).truckModel}</span>
                                                </div>
                                            ) : (
                                                <span className="text-muted-foreground text-xs italic">Unassigned</span>
                                            )}
                                        </TableCell>
                                        <TableCell>
                                            <span className="font-mono text-xs">{driver.truckLicenseId}</span>
                                        </TableCell>
                                        <TableCell>
                                            <span className="text-sm text-muted-foreground">{driver.licenseType || "-"}</span>
                                        </TableCell>
                                        <TableCell>
                                            <Badge variant="secondary" className={`${getStatusColor(driver.status)} font-medium border`}>
                                                <span className="w-1.5 h-1.5 rounded-full bg-current mr-1.5" />
                                                {driver.status}
                                            </Badge>
                                        </TableCell>
                                        <TableCell className="text-right">
                                            <DropdownMenu>
                                                <DropdownMenuTrigger asChild>
                                                    <Button variant="ghost" className="h-8 w-8 p-0">
                                                        <MoreHorizontal className="h-4 w-4" />
                                                    </Button>
                                                </DropdownMenuTrigger>
                                                <DropdownMenuContent align="end">
                                                    <DropdownMenuLabel>Actions</DropdownMenuLabel>
                                                    <DropdownMenuItem onClick={() => router.push(`/admin/drivers/view?id=${driver.id}`)}>View Profile</DropdownMenuItem>
                                                    <DropdownMenuItem onClick={() => router.push(`/admin/drivers/edit?id=${driver.id}`)}>Edit Details</DropdownMenuItem>
                                                </DropdownMenuContent>
                                            </DropdownMenu>
                                        </TableCell>
                                    </TableRow>
                                ))
                            )}
                        </TableBody>
                    </Table>

                    {/* Pagination Footer */}
                    <div className="flex items-center justify-between px-4 py-4 border-t border-border/50 bg-muted/20">
                        <div className="text-sm text-muted-foreground">
                            Showing {paginatedDrivers.length > 0 ? (currentPage - 1) * itemsPerPage + 1 : 0} to {Math.min(currentPage * itemsPerPage, filteredDrivers.length)} of {filteredDrivers.length} entries
                        </div>
                        <div className="flex items-center gap-2">
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setCurrentPage(p => Math.max(1, p - 1))}
                                disabled={currentPage === 1}
                            >
                                Previous
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setCurrentPage(p => Math.min(totalPages, p + 1))}
                                disabled={currentPage === totalPages || totalPages === 0}
                            >
                                Next
                            </Button>
                        </div>
                    </div>
                </div>
            </div>
        </div>
    );
}
