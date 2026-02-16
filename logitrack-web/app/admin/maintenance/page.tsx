"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { MaintenanceDashboardData, getMaintenanceOverview } from "./actions.client";
import { useLanguage } from "@/context/language";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { MoreHorizontal, Wrench, Search, Loader2, DollarSign, Activity, AlertTriangle, FileText } from "lucide-react";
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuTrigger
} from "@/components/ui/dropdown-menu";
import Link from "next/link";
import { format } from "date-fns";
import { Select, SelectTrigger, SelectValue, SelectContent, SelectItem } from "@/components/ui/select";

export default function MaintenanceDashboardPage() {
    const [records, setRecords] = useState<MaintenanceDashboardData[]>([]);
    const [loading, setLoading] = useState(true);
    const [searchTerm, setSearchTerm] = useState("");
    const [typeFilter, setTypeFilter] = useState<"all" | "PM" | "CM">("all");
    const { t } = useLanguage();
    const router = useRouter();

    useEffect(() => {
        async function loadData() {
            setLoading(true);
            const data = await getMaintenanceOverview();
            setRecords(data);
            setLoading(false);
        }
        loadData();
    }, []);

    // Derived stats
    const totalRecords = records.length;
    const totalCost = records.reduce((sum, r) => sum + (r.totalCost || 0), 0);
    const pmCost = records.filter(r => r.type === "PM").reduce((sum, r) => sum + (r.totalCost || 0), 0);
    const cmCost = records.filter(r => r.type === "CM").reduce((sum, r) => sum + (r.totalCost || 0), 0);
    const activeJobs = records.filter(r => r.status === "in_progress").length;

    // Filter Logic
    const filteredRecords = records.filter(record => {
        const matchesSearch =
            record.truckLicensePlate.toLowerCase().includes(searchTerm.toLowerCase()) ||
            record.truckBrand.toLowerCase().includes(searchTerm.toLowerCase()) ||
            record.serviceType.toLowerCase().includes(searchTerm.toLowerCase());

        const matchesType = typeFilter === "all" || record.type === typeFilter;

        return matchesSearch && matchesType;
    });


    if (loading) {
        return <div className="flex h-screen items-center justify-center"><Loader2 className="h-8 w-8 animate-spin text-primary" /></div>;
    }

    return (
        <div className="container mx-auto p-6 space-y-8 max-w-[1400px]">
            {/* Header */}
            <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("maintenance.title")}</h1>
                    <p className="text-muted-foreground mt-1">{t("maintenance.subtitle")}</p>
                </div>
                <div className="flex gap-2">
                    {/* Placeholder for export or add buttons if needed */}
                </div>
            </div>

            {/* Summary Cards */}
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("maintenance.totalExpenses")}</CardTitle>
                        <DollarSign className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">฿{totalCost.toLocaleString()}</div>
                        <p className="text-xs text-muted-foreground">{t("maintenance.lifetimeSpend")}</p>
                        <div className="mt-2 pt-2 border-t text-xs flex justify-between items-center text-muted-foreground">
                            <span>{t("maintenance.avgPerRecord")}:</span>
                            <span className="font-semibold text-foreground">฿{totalRecords > 0 ? Math.round(totalCost / totalRecords).toLocaleString() : 0}</span>
                        </div>
                    </CardContent>
                </Card>

                <Card className="border-l-4 border-l-blue-500">
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-blue-700">{t("maintenance.pmCosts")}</CardTitle>
                        <FileText className="h-4 w-4 text-blue-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-blue-700">฿{pmCost.toLocaleString()}</div>
                        <p className="text-xs text-blue-600 font-medium">{t("maintenance.preventiveMaintenance")}</p>
                        <p className="text-xs text-muted-foreground mt-1">
                            {((pmCost / totalCost) * 100).toFixed(1)}% {t("maintenance.ofTotal")}
                        </p>
                    </CardContent>
                </Card>

                <Card className="border-l-4 border-l-orange-500">
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-orange-700">{t("maintenance.cmCosts")}</CardTitle>
                        <Wrench className="h-4 w-4 text-orange-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-orange-700">฿{cmCost.toLocaleString()}</div>
                        <p className="text-xs text-orange-600 font-medium">{t("maintenance.correctiveRepairs")}</p>
                        <p className="text-xs text-muted-foreground mt-1">
                            {((cmCost / totalCost) * 100).toFixed(1)}% {t("maintenance.ofTotal")}
                        </p>
                    </CardContent>
                </Card>

                <Card className={activeJobs > 0 ? "animate-pulse border-yellow-400 border" : ""}>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("maintenance.activeJobs")}</CardTitle>
                        <Activity className="h-4 w-4 text-green-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">{activeJobs}</div>
                        <p className="text-xs text-muted-foreground">{t("maintenance.vehiclesInShop")}</p>
                        {activeJobs > 0 && (
                            <div className="mt-2 text-xs text-orange-600 font-medium">
                                {t("maintenance.requiresAttention")}
                            </div>
                        )}
                    </CardContent>
                </Card>
            </div>

            {/* Main Table */}
            <Card>
                <CardHeader>
                    <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4">
                        <CardTitle>{t("maintenance.serviceHistory")}</CardTitle>
                        <div className="flex gap-2 w-full md:w-auto">
                            <Select value={typeFilter} onValueChange={(v: any) => setTypeFilter(v)}>
                                <SelectTrigger className="w-[150px]">
                                    <SelectValue placeholder={t("maintenance.filterType")} />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="all">{t("maintenance.allTypes")}</SelectItem>
                                    <SelectItem value="PM">{t("maintenance.form.pm")}</SelectItem>
                                    <SelectItem value="CM">{t("maintenance.form.cm")}</SelectItem>
                                </SelectContent>
                            </Select>
                            <div className="relative flex-1 md:w-[300px]">
                                <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
                                <Input
                                    placeholder={t("maintenance.searchPlaceholder")}
                                    className="pl-9"
                                    value={searchTerm}
                                    onChange={(e) => setSearchTerm(e.target.value)}
                                />
                            </div>
                        </div>
                    </div>
                </CardHeader>
                <CardContent>
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("maintenance.table.date")}</TableHead>
                                <TableHead>{t("maintenance.table.vehicle")}</TableHead>
                                <TableHead>{t("maintenance.table.type")}</TableHead>
                                <TableHead>{t("maintenance.table.service")}</TableHead>
                                <TableHead className="text-right">{t("maintenance.table.labor")}</TableHead>
                                <TableHead className="text-right">{t("maintenance.table.parts")}</TableHead>
                                <TableHead className="text-right">{t("maintenance.table.total")}</TableHead>
                                <TableHead>{t("maintenance.table.status")}</TableHead>
                                <TableHead className="text-right">{t("maintenance.table.actions")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {filteredRecords.map((record) => (
                                <TableRow
                                    key={record.id}
                                    className="cursor-pointer hover:bg-muted/50"
                                    onClick={() => router.push(`/admin/trucks/maintenance?id=${record.truckId}`)}
                                >
                                    <TableCell className="whitespace-nowrap font-mono text-xs">
                                        {format(new Date(record.startDate), "dd MMM yyyy")}
                                    </TableCell>
                                    <TableCell>
                                        <div className="font-semibold">{record.truckLicensePlate}</div>
                                        <div className="text-xs text-muted-foreground">{record.truckBrand}</div>
                                    </TableCell>
                                    <TableCell>
                                        <Badge variant={record.type === 'PM' ? "default" : "destructive"} className={record.type === 'PM' ? "bg-blue-600" : ""}>
                                            {record.type}
                                        </Badge>
                                    </TableCell>
                                    <TableCell className="max-w-[200px] truncate" title={record.serviceType}>
                                        {t(`maintenance.service.${record.serviceType}`) !== `maintenance.service.${record.serviceType}` ? t(`maintenance.service.${record.serviceType}`) : record.serviceType}
                                    </TableCell>
                                    <TableCell className="text-right text-muted-foreground">
                                        {record.costLabor ? `฿${record.costLabor.toLocaleString()}` : "-"}
                                    </TableCell>
                                    <TableCell className="text-right text-muted-foreground">
                                        {record.costParts ? `฿${record.costParts.toLocaleString()}` : "-"}
                                    </TableCell>
                                    <TableCell className="text-right font-bold">
                                        {record.totalCost ? `฿${record.totalCost.toLocaleString()}` : "-"}
                                    </TableCell>
                                    <TableCell>
                                        {record.status === 'completed' ? (
                                            <Badge variant="outline" className="text-green-600 border-green-200 bg-green-50">{t("maintenance.form.completed")}</Badge>
                                        ) : (
                                            <Badge variant="secondary" className="bg-yellow-100 text-yellow-800 hover:bg-yellow-200">{t("maintenance.form.inProgress")}</Badge>
                                        )}
                                    </TableCell>
                                    <TableCell className="text-right">
                                        <DropdownMenu>
                                            <DropdownMenuTrigger asChild>
                                                <Button variant="ghost" className="h-8 w-8 p-0">
                                                    <span className="sr-only">{t("maintenance.history.openMenu")}</span>
                                                    <MoreHorizontal className="h-4 w-4" />
                                                </Button>
                                            </DropdownMenuTrigger>
                                            <DropdownMenuContent align="end">
                                                <DropdownMenuLabel>{t("maintenance.table.actions")}</DropdownMenuLabel>
                                                <DropdownMenuItem asChild>
                                                    <Link href={`/admin/trucks/maintenance?id=${record.truckId}`} className="flex items-center cursor-pointer">
                                                        <Wrench className="mr-2 h-4 w-4" />
                                                        {t("maintenance.manageUpdate")}
                                                    </Link>
                                                </DropdownMenuItem>
                                            </DropdownMenuContent>
                                        </DropdownMenu>
                                    </TableCell>
                                </TableRow>
                            ))}
                            {filteredRecords.length === 0 && (
                                <TableRow>
                                    <TableCell colSpan={9} className="h-24 text-center">
                                        {t("maintenance.noRecords")}
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
