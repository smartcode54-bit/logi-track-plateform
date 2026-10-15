"use client";

import { useState } from "react";
import { useRenewals } from "@/features/trucks/api/useTrucks";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { toneBadgeVariant } from "@/lib/statusPresentation";
import { th } from "date-fns/locale";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { MoreHorizontal, Truck, AlertTriangle, CheckCircle2, Search, Calendar, Shield, CreditCard } from "lucide-react";
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
import { TruckQueryLoading } from "@/features/trucks/components/TruckQueryLoading";
import { TruckQueryError } from "@/features/trucks/components/TruckQueryError";
import { truckTypeLabel } from "@/features/trucks/utils/labels";
import { PageHeader } from "@/components/page-header";

export function RenewalsPage() {
    const { t, language } = useLanguage();
    // `['renewals', {}]` (T25): `GET /v1/trucks/renewals` (state on the Bangkok calendar) or Firestore.
    const query = useRenewals();
    const { renewals: trucks, isPending: loading } = query;
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
            truckTypeLabel(truck.type, t).toLowerCase().includes(searchTerm.toLowerCase()) ||
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


    if (loading || query.isError && !query.data) return <div className="flex flex-col gap-section">
        <PageHeader title={t("nav.truckRenewals")} />
        {loading ? <TruckQueryLoading label={t("common.loading")} /> : <TruckQueryError onRetry={() => void query.refetch()} retrying={query.isFetching} />}
    </div>;

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
                            <Badge variant={filter === "critical" ? "outline" : "secondary"} className="ml-1 bg-background">{taxOverdue + insuranceOverdue}</Badge>
                        </Button>
                        <Button variant={filter === "warning" ? "secondary" : "outline"} aria-pressed={filter === "warning"} onClick={() => setFilter("warning")} className="gap-2">
                            <Calendar className="h-4 w-4 text-warning" aria-hidden />
                            {t("renewals.filter.warning")}
                            <Badge variant={filter === "warning" ? "outline" : "secondary"} className="ml-1 bg-background">{taxExpiring + insuranceExpiring}</Badge>
                        </Button>
                    </>
                }
            />

            {query.isError && <TruckQueryError onRetry={() => void query.refetch()} retrying={query.isFetching} />}
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

                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-destructive">{t("renewals.card.taxCritical")}</CardTitle>
                        <CreditCard className="h-4 w-4 text-destructive" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-destructive">{taxOverdue}</div>
                        <p className="text-xs text-destructive font-medium">{t("renewals.card.overduePayments")}</p>
                        <p className="text-xs text-muted-foreground mt-1">{taxExpiring} {t("renewals.card.expiringSoon")}</p>
                    </CardContent>
                </Card>

                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-destructive">{t("renewals.card.insuranceCritical")}</CardTitle>
                        <Shield className="h-4 w-4 text-destructive" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-destructive">{insuranceOverdue}</div>
                        <p className="text-xs text-destructive font-medium">{t("renewals.card.policiesExpired")}</p>
                        <p className="text-xs text-muted-foreground mt-1">{insuranceExpiring} {t("renewals.card.expiringSoon")}</p>
                    </CardContent>
                </Card>

                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("renewals.card.complianceRate")}</CardTitle>
                        <CheckCircle2 className="h-4 w-4 text-success" />
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
                    <div className="flex flex-col gap-4 md:flex-row md:items-center md:justify-between">
                        <CardTitle>{t("renewals.table.title")}</CardTitle>
                        <div className="relative w-full md:w-[300px]">
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
                                        <div className="text-xs text-muted-foreground">{truckTypeLabel(truck.type, t)} · {truck.province}</div>
                                    </TableCell>
                                    <TableCell className="text-center">
                                        <Badge variant="outline" className="capitalize text-xs">
                                            {truck.ownershipType === "own" ? t("trucks.detail.ownFleet") : t("trucks.detail.subcontractorFleet")}
                                        </Badge>
                                    </TableCell>
                                    <TableCell>
                                        <div className="flex flex-col gap-1">
                                            <StatusBadge status={truck.tax.state} />
                                            <span className="text-xs text-muted-foreground">
                                                {truck.tax.expiryDate ? format(new Date(truck.tax.expiryDate), "dd MMM yyyy", { locale: language === "th" ? th : undefined }) : t("masterData.renewals.noDate")}
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
                                                {truck.insurance.expiryDate ? format(new Date(truck.insurance.expiryDate), "dd MMM yyyy", { locale: language === "th" ? th : undefined }) : t("masterData.renewals.noDate")}
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
                                                    <span className="sr-only">{t("common.actionsForRow")}</span>
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
        return <Badge variant={toneBadgeVariant("primary")} className="w-fit">{t("renewals.status.inProgress")}</Badge>;
    }
    if (status === "overdue") {
        return <Badge variant={toneBadgeVariant("destructive")} className="w-fit">{t("renewals.status.overdue")}</Badge>;
    }
    if (status === "expiring_soon") {
        return <Badge variant={toneBadgeVariant("warning")} className="w-fit">{t("renewals.status.expiringSoon")}</Badge>;
    }
    return <Badge variant={toneBadgeVariant("success")} className="w-fit">{t("renewals.status.ok")}</Badge>;
}
