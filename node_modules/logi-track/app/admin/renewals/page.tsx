"use client";

import { useEffect, useState } from "react";
import { RenewalTruckData, getRenewalOverview } from "./actions.client";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { MoreHorizontal, Truck, AlertTriangle, CheckCircle2, Search, Calendar, Shield, CreditCard, Loader2, ArrowRight } from "lucide-react";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuTrigger
} from "@/components/ui/dropdown-menu";
import Link from "next/link";
import { format } from "date-fns";

export default function RenewalsPage() {
    const [trucks, setTrucks] = useState<RenewalTruckData[]>([]);
    const [loading, setLoading] = useState(true);
    const [searchTerm, setSearchTerm] = useState("");
    const [filter, setFilter] = useState<"all" | "critical" | "warning">("all");

    useEffect(() => {
        async function loadData() {
            setLoading(true);
            const data = await getRenewalOverview();
            setTrucks(data);
            setLoading(false);
        }
        loadData();
    }, []);

    // Derived stats
    const totalTrucks = trucks.length;
    const taxOverdue = trucks.filter(t => t.taxStatus === "overdue").length;
    const taxExpiring = trucks.filter(t => t.taxStatus === "expiring_soon").length;
    const insuranceOverdue = trucks.filter(t => t.insuranceStatus === "overdue").length;
    const insuranceExpiring = trucks.filter(t => t.insuranceStatus === "expiring_soon").length;

    // Calculate total estimated cost
    const totalCost = trucks.reduce((sum, t) => sum + (t.taxExpense || 0) + (t.insurancePremium || 0), 0);

    // Filter Logic
    const filteredTrucks = trucks.filter(truck => {
        const matchesSearch =
            truck.licensePlate.toLowerCase().includes(searchTerm.toLowerCase()) ||
            truck.brand.toLowerCase().includes(searchTerm.toLowerCase());

        if (!matchesSearch) return false;

        if (filter === "critical") {
            return truck.taxStatus === "overdue" || truck.insuranceStatus === "overdue";
        }
        if (filter === "warning") {
            return truck.taxStatus === "expiring_soon" || truck.insuranceStatus === "expiring_soon";
        }

        return true;
    });


    if (loading) {
        return <div className="flex h-screen items-center justify-center"><Loader2 className="h-8 w-8 animate-spin text-primary" /></div>;
    }

    return (
        <div className="container mx-auto p-6 space-y-8 max-w-[1400px]">
            {/* Header */}
            <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">Fleet Compliance & Renewals</h1>
                    <p className="text-muted-foreground mt-1">Manage Tax and Insurance expiration across the fleet.</p>
                </div>
                <div className="flex gap-2">
                    <Button variant="outline" onClick={() => setFilter("all")} className={filter === "all" ? "bg-accent" : ""}>
                        All Vehicles
                    </Button>
                    <Button variant="outline" onClick={() => setFilter("critical")} className={`gap-2 ${filter === "critical" ? "bg-red-100 text-red-900 border-red-200 hover:bg-red-200" : ""}`}>
                        <AlertTriangle className="h-4 w-4 text-red-600" />
                        Critical Action
                        <Badge variant="secondary" className="ml-1 bg-red-200 text-red-800">{taxOverdue + insuranceOverdue}</Badge>
                    </Button>
                    <Button variant="outline" onClick={() => setFilter("warning")} className={`gap-2 ${filter === "warning" ? "bg-yellow-100 text-yellow-900 border-yellow-200 hover:bg-yellow-200" : ""}`}>
                        <Calendar className="h-4 w-4 text-yellow-600" />
                        Expiring Soon
                        <Badge variant="secondary" className="ml-1 bg-yellow-200 text-yellow-800">{taxExpiring + insuranceExpiring}</Badge>
                    </Button>
                </div>
            </div>

            {/* Summary Cards */}
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">Total Fleet</CardTitle>
                        <Truck className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">{totalTrucks}</div>
                        <p className="text-xs text-muted-foreground">Active vehicles</p>
                        <div className="mt-2 pt-2 border-t text-xs flex justify-between items-center text-muted-foreground">
                            <span>Total Est. Cost:</span>
                            <span className="font-semibold text-foreground">฿{totalCost.toLocaleString()}</span>
                        </div>
                    </CardContent>
                </Card>

                <Card className="border-l-4 border-l-red-500">
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-red-700">Tax Critical</CardTitle>
                        <CreditCard className="h-4 w-4 text-red-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-red-700">{taxOverdue}</div>
                        <p className="text-xs text-red-600 font-medium">Overdue payments</p>
                        <p className="text-xs text-muted-foreground mt-1">{taxExpiring} expiring soon</p>
                    </CardContent>
                </Card>

                <Card className="border-l-4 border-l-orange-500">
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-orange-700">Insurance Critical</CardTitle>
                        <Shield className="h-4 w-4 text-orange-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-orange-700">{insuranceOverdue}</div>
                        <p className="text-xs text-orange-600 font-medium">Policies expired</p>
                        <p className="text-xs text-muted-foreground mt-1">{insuranceExpiring} expiring soon</p>
                    </CardContent>
                </Card>

                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">Compliance Rate</CardTitle>
                        <CheckCircle2 className="h-4 w-4 text-green-500" />
                    </CardHeader>
                    <CardContent>
                        {/* Simple compliance calc */}
                        <div className="text-2xl font-bold">
                            {trucks.length > 0 ? Math.round(((totalTrucks - (taxOverdue + insuranceOverdue)) / totalTrucks) * 100) : 0}%
                        </div>
                        <p className="text-xs text-muted-foreground">Operating fully legally</p>
                    </CardContent>
                </Card>
            </div>

            {/* Main Table */}
            <Card>
                <CardHeader>
                    <div className="flex justify-between items-center">
                        <CardTitle>Vehicle Renewal Status</CardTitle>
                        <div className="relative w-[300px]">
                            <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
                            <Input
                                placeholder="Search by license or brand..."
                                className="pl-9"
                                value={searchTerm}
                                onChange={(e) => setSearchTerm(e.target.value)}
                            />
                        </div>
                    </div>
                </CardHeader>
                <CardContent>
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>Vehicle</TableHead>
                                <TableHead>Ownership</TableHead>
                                <TableHead>Tax Status</TableHead>
                                <TableHead className="text-right">Tax Cost</TableHead>
                                <TableHead>Insurance Status</TableHead>
                                <TableHead className="text-right">Ins. Cost</TableHead>
                                <TableHead className="text-right">Actions</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {filteredTrucks.map((truck) => (
                                <TableRow key={truck.id}>
                                    <TableCell>
                                        <div className="font-semibold">{truck.licensePlate}</div>
                                        <div className="text-xs text-muted-foreground">{truck.brand} {truck.model}</div>
                                    </TableCell>
                                    <TableCell>
                                        <Badge variant="outline" className="capitalize text-xs">
                                            {truck.ownershipType}
                                        </Badge>
                                    </TableCell>
                                    <TableCell>
                                        <div className="flex flex-col gap-1">
                                            <StatusBadge status={truck.taxStatus} label="Tax" />
                                            <span className="text-xs text-muted-foreground">
                                                {truck.taxExpiryDate ? format(new Date(truck.taxExpiryDate), "dd MMM yyyy") : "No Data"}
                                            </span>
                                        </div>
                                    </TableCell>
                                    <TableCell className="text-right font-medium">
                                        {truck.taxExpense ? `฿${truck.taxExpense.toLocaleString()}` : "-"}
                                    </TableCell>
                                    <TableCell>
                                        <div className="flex flex-col gap-1">
                                            <StatusBadge status={truck.insuranceStatus} label="Insurance" />
                                            <span className="text-xs text-muted-foreground">
                                                {truck.insuranceExpiryDate ? format(new Date(truck.insuranceExpiryDate), "dd MMM yyyy") : "No Data"}
                                            </span>
                                        </div>
                                    </TableCell>
                                    <TableCell className="text-right font-medium">
                                        {truck.insurancePremium ? `฿${truck.insurancePremium.toLocaleString()}` : "-"}
                                    </TableCell>
                                    <TableCell className="text-right">
                                        <DropdownMenu>
                                            <DropdownMenuTrigger asChild>
                                                <Button variant="ghost" className="h-8 w-8 p-0">
                                                    <span className="sr-only">Open menu</span>
                                                    <MoreHorizontal className="h-4 w-4" />
                                                </Button>
                                            </DropdownMenuTrigger>
                                            <DropdownMenuContent align="end">
                                                <DropdownMenuLabel>Actions</DropdownMenuLabel>
                                                <DropdownMenuItem asChild>
                                                    <Link href={`/admin/trucks/renew?id=${truck.id}&type=tax`}>
                                                        Renew Tax
                                                    </Link>
                                                </DropdownMenuItem>
                                                <DropdownMenuItem asChild>
                                                    <Link href={`/admin/trucks/renew?id=${truck.id}&type=insurance`}>
                                                        Renew Insurance
                                                    </Link>
                                                </DropdownMenuItem>
                                                <DropdownMenuSeparator />
                                                <DropdownMenuItem asChild>
                                                    <Link href={`/admin/trucks/view?id=${truck.id}`}>
                                                        View Details
                                                    </Link>
                                                </DropdownMenuItem>
                                            </DropdownMenuContent>
                                        </DropdownMenu>
                                    </TableCell>
                                </TableRow>
                            ))}
                            {filteredTrucks.length === 0 && (
                                <TableRow>
                                    <TableCell colSpan={7} className="h-24 text-center">
                                        No vehicles found matching criteria.
                                    </TableCell>
                                </TableRow>
                            )}
                        </TableBody>
                    </Table>
                </CardContent>
            </Card>
        </div>
    );
}

function StatusBadge({ status, label }: { status: string, label: string }) {
    if (status === "in_progress") {
        return <Badge variant="secondary" className="w-fit text-[10px] h-5 px-1.5 bg-blue-100 text-blue-700 hover:bg-blue-100 border-blue-200">IN PROGRESS</Badge>
    }
    if (status === "overdue") {
        return <Badge variant="destructive" className="w-fit text-[10px] h-5 px-1.5 bg-red-100 text-red-700 hover:bg-red-100 border-red-200">OVERDUE</Badge>
    }
    if (status === "expiring_soon") {
        return <Badge className="w-fit text-[10px] h-5 px-1.5 bg-yellow-100 text-yellow-700 hover:bg-yellow-100 border-yellow-200">EXPIRING SOON</Badge>
    }
    return <Badge variant="outline" className="w-fit text-[10px] h-5 px-1.5 bg-green-50 text-green-700 border-green-200 hover:bg-green-50">OK</Badge>
}
