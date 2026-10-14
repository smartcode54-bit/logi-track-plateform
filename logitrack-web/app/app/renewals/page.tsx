"use client";

import { useState } from "react";
import { useRenewals } from "@/features/trucks/api/useTrucks";
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
import { useLanguage } from "@/context/language";
import { PageHeader } from "@/components/page-header";

export default function RenewalsPage() {
    const { t } = useLanguage();
    // `['renewals', {}]` (T25): `GET /v1/trucks/renewals` (state on the Bangkok calendar) or Firestore.
    const { renewals: trucks, isPending: loading } = useRenewals();
    const [searchTerm, setSearchTerm] = useState("");
    const [filter, setFilter] = useState<"all" | "critical" | "warning">("all");

    // Derived stats
    const totalTrucks = trucks.length;
    const taxOverdue = trucks.filter(t => t.tax.state === "overdue").length;
    const taxExpiring = trucks.filter(t => t.tax.state === "expiring_soon").length;
    const insuranceOverdue = trucks.filter(t => t.insurance.state === "overdue").length;
    const insuranceExpiring = trucks.filter(t => t.insurance.state === "expiring_soon").length;

    // Calculate total estimated cost
    const totalCost = trucks.reduce((sum, t) => sum + (t.tax.amountThb || 0) + (t.insurance.amountThb || 0), 0);

    // Renewal rate: items renewed on time / total items needing renewal
    const totalRenewalItems = totalTrucks * 2; // each truck has tax + insurance
    const renewedOnTime = trucks.filter(t => t.tax.state === "ok").length + trucks.filter(t => t.insurance.state === "ok").length;
    const renewalRate = totalRenewalItems > 0 ? ((renewedOnTime / totalRenewalItems) * 100).toFixed(2) : "0.00";

    // Filter Logic
    const filteredTrucks = trucks.filter(truck => {
        const matchesSearch =
            truck.licensePlate.toLowerCase().includes(searchTerm.toLowerCase()) ||
            truck.type.toLowerCase().includes(searchTerm.toLowerCase()) ||
            truck.province.toLowerCase().includes(searchTerm.toLowerCase());

        if (!matchesSearch) return false;

        if (filter === "critical") {
            return truck.tax.state === "overdue" || truck.insurance.state === "overdue";
        }
        if (filter === "warning") {
            return truck.tax.state === "expiring_soon" || truck.insurance.state === "expiring_soon";
        }

        return true;
    });


    if (loading) {
        return <div className="flex h-screen items-center justify-center"><Loader2 className="h-8 w-8 animate-spin text-primary" /></div>;
    }

    return (
        <div className="flex flex-col gap-section">
            <PageHeader
                title={t("nav.truckRenewals")}
                description={t("renewals.subtitle")}
                actions={
                    <>
                        <Button variant={filter === "all" ? "secondary" : "outline"} aria-pressed={filter === "all"} onClick={() => setFilter("all")}>
                            {t("renewals.filter.all")}
                        </Button>
                        <Button variant={filter === "critical" ? "secondary" : "outline"} aria-pressed={filter === "critical"} onClick={() => setFilter("critical")} className="gap-2">
                            <AlertTriangle className="h-4 w-4 text-destructive" aria-hidden />
                            {t("renewals.filter.critical")}
                            <Badge variant="secondary" className="ml-1">{taxOverdue + insuranceOverdue}</Badge>
                        </Button>
                        <Button variant={filter === "warning" ? "secondary" : "outline"} aria-pressed={filter === "warning"} onClick={() => setFilter("warning")} className="gap-2">
                            <Calendar className="h-4 w-4 text-warning" aria-hidden />
                            {t("renewals.filter.warning")}
                            <Badge variant="secondary" className="ml-1">{taxExpiring + insuranceExpiring}</Badge>
                        </Button>
                    </>
                }
            />

            {/* Summary Cards */}
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("renewals.card.totalFleet")}</CardTitle>
                        <Truck className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">{totalTrucks}</div>
                        <p className="text-xs text-muted-foreground">{t("renewals.card.activeVehicles")}</p>
                        <div className="mt-2 pt-2 border-t text-xs flex justify-between items-center text-muted-foreground">
                            <span>{t("renewals.card.totalEstCost")}:</span>
                            <span className="font-semibold text-foreground">฿{totalCost.toLocaleString()}</span>
                        </div>
                    </CardContent>
                </Card>

                <Card className="border-l-4 border-l-red-500">
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-red-700">{t("renewals.card.taxCritical")}</CardTitle>
                        <CreditCard className="h-4 w-4 text-red-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-red-700">{taxOverdue}</div>
                        <p className="text-xs text-red-600 font-medium">{t("renewals.card.overduePayments")}</p>
                        <p className="text-xs text-muted-foreground mt-1">{taxExpiring} {t("renewals.card.expiringSoon")}</p>
                    </CardContent>
                </Card>

                <Card className="border-l-4 border-l-orange-500">
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-orange-700">{t("renewals.card.insuranceCritical")}</CardTitle>
                        <Shield className="h-4 w-4 text-orange-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-orange-700">{insuranceOverdue}</div>
                        <p className="text-xs text-orange-600 font-medium">{t("renewals.card.policiesExpired")}</p>
                        <p className="text-xs text-muted-foreground mt-1">{insuranceExpiring} {t("renewals.card.expiringSoon")}</p>
                    </CardContent>
                </Card>

                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("renewals.card.complianceRate")}</CardTitle>
                        <CheckCircle2 className="h-4 w-4 text-green-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">
                            {renewalRate}%
                        </div>
                        <p className="text-xs text-muted-foreground">{t("renewals.card.legalOperation")}</p>
                    </CardContent>
                </Card>
            </div>

            {/* Main Table */}
            <Card>
                <CardHeader>
                    <div className="flex justify-between items-center">
                        <CardTitle>{t("renewals.table.title")}</CardTitle>
                        <div className="relative w-[300px]">
                            <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
                            <Input
                                placeholder={t("renewals.searchPlaceholder")}
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
                                <TableHead className="w-[300px]">{t("renewals.table.vehicle")}</TableHead>
                                <TableHead className="w-[150px] text-center">{t("renewals.table.ownership")}</TableHead>
                                <TableHead className="w-[200px]">{t("renewals.table.taxStatus")}</TableHead>
                                <TableHead className="w-[150px] text-right">{t("renewals.table.taxCost")}</TableHead>
                                <TableHead className="w-[200px]">{t("renewals.table.insuranceStatus")}</TableHead>
                                <TableHead className="w-[150px] text-right">{t("renewals.table.insuranceCost")}</TableHead>
                                <TableHead className="w-[100px] text-right">{t("renewals.table.actions")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {filteredTrucks.map((truck) => (
                                <TableRow key={truck.truckId}>
                                    <TableCell>
                                        <div className="font-semibold">{truck.licensePlate}</div>
                                        <div className="text-xs text-muted-foreground">{truck.type} · {truck.province}</div>
                                    </TableCell>
                                    <TableCell className="text-center">
                                        <Badge variant="outline" className="capitalize text-xs">
                                            {truck.ownershipType}
                                        </Badge>
                                    </TableCell>
                                    <TableCell>
                                        <div className="flex flex-col gap-1">
                                            <StatusBadge status={truck.tax.state} />
                                            <span className="text-xs text-muted-foreground">
                                                {truck.tax.expiryDate ? format(new Date(truck.tax.expiryDate), "dd MMM yyyy") : t("masterData.renewals.noDate")}
                                            </span>
                                        </div>
                                    </TableCell>
                                    <TableCell className="text-right font-medium">
                                        {truck.tax.amountThb ? `฿${truck.tax.amountThb.toLocaleString()}` : "-"}
                                    </TableCell>
                                    <TableCell>
                                        <div className="flex flex-col gap-1">
                                            <StatusBadge status={truck.insurance.state} />
                                            <span className="text-xs text-muted-foreground">
                                                {truck.insurance.expiryDate ? format(new Date(truck.insurance.expiryDate), "dd MMM yyyy") : t("masterData.renewals.noDate")}
                                            </span>
                                        </div>
                                    </TableCell>
                                    <TableCell className="text-right font-medium">
                                        {truck.insurance.amountThb ? `฿${truck.insurance.amountThb.toLocaleString()}` : "-"}
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
                                                <DropdownMenuLabel>{t("renewals.table.actions")}</DropdownMenuLabel>
                                                <DropdownMenuItem asChild>
                                                    <Link href={`/app/trucks/renew?id=${truck.truckId}&type=tax`} prefetch={false}>
                                                        {t("renewals.action.renewTax")}
                                                    </Link>
                                                </DropdownMenuItem>
                                                <DropdownMenuItem asChild>
                                                    <Link href={`/app/trucks/renew?id=${truck.truckId}&type=insurance`} prefetch={false}>
                                                        {t("renewals.action.renewInsurance")}
                                                    </Link>
                                                </DropdownMenuItem>
                                                <DropdownMenuSeparator />
                                                <DropdownMenuItem asChild>
                                                    <Link href={`/app/trucks/view?id=${truck.truckId}`} prefetch={false}>
                                                        {t("renewals.action.viewDetails")}
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
                                        {t("renewals.table.noData")}
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

function StatusBadge({ status }: { status: string }) {
    const { t } = useLanguage();
    if (status === "in_progress") {
        return <Badge variant="secondary" className="w-fit text-[10px] h-5 px-1.5 bg-blue-100 text-blue-700 hover:bg-blue-100 border-blue-200">{t("renewals.status.inProgress")}</Badge>
    }
    if (status === "overdue") {
        return <Badge variant="destructive" className="w-fit text-[10px] h-5 px-1.5 bg-red-100 text-red-700 hover:bg-red-100 border-red-200">{t("renewals.status.overdue")}</Badge>
    }
    if (status === "expiring_soon") {
        return <Badge className="w-fit text-[10px] h-5 px-1.5 bg-yellow-100 text-yellow-700 hover:bg-yellow-100 border-yellow-200">{t("renewals.status.expiringSoon")}</Badge>
    }
    return <Badge variant="outline" className="w-fit text-[10px] h-5 px-1.5 bg-green-50 text-green-700 border-green-200 hover:bg-green-50">{t("renewals.status.ok")}</Badge>
}
