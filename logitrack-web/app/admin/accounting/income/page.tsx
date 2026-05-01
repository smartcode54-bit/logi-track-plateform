"use client";

import { useEffect, useMemo, useState } from "react";
import { format } from "date-fns";
import { collection, getDocs, query, orderBy, limit } from "firebase/firestore";
import { DollarSign, Hash, Loader2, RefreshCw, TrendingUp } from "lucide-react";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useLanguage } from "@/context/language";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

interface IncomeRow {
    id: string;
    spxTripId?: string;
    billingEstimateThb?: number;
    billingBaseRateThb?: number;
    billingRateImportId?: string;
    customerId?: string;
    deliveredTimestamp?: Date;
}

function toDate(value: unknown): Date | undefined {
    if (!value) return undefined;
    if (value instanceof Date) return value;
    if (typeof (value as { toDate?: () => Date }).toDate === "function") {
        return (value as { toDate: () => Date }).toDate();
    }
    if (typeof value === "string" || typeof value === "number") {
        const d = new Date(value);
        return Number.isNaN(d.getTime()) ? undefined : d;
    }
    return undefined;
}

function toNumber(value: unknown): number | undefined {
    const n = Number(value);
    return Number.isFinite(n) ? n : undefined;
}

export default function AccountingIncomePage() {
    const { t } = useLanguage();
    const [loading, setLoading] = useState(true);
    const [rows, setRows] = useState<IncomeRow[]>([]);

    const loadData = async () => {
        setLoading(true);
        try {
            const snap = await getDocs(
                query(
                    collection(db, COLLECTIONS.TRIP_RECORDS),
                    orderBy("createdAt", "desc"),
                    limit(500)
                )
            );
            const nextRows = snap.docs
                .map((docSnap) => {
                    const d = docSnap.data();
                    return {
                        id: docSnap.id,
                        spxTripId: d.spxTripId ? String(d.spxTripId) : undefined,
                        billingEstimateThb: toNumber(d.billingEstimateThb),
                        billingBaseRateThb: toNumber(d.billingBaseRateThb),
                        billingRateImportId: d.billingRateImportId ? String(d.billingRateImportId) : undefined,
                        customerId: d.sourceHubLinkedCustomerId
                            ? String(d.sourceHubLinkedCustomerId)
                            : undefined,
                        deliveredTimestamp: toDate(d.deliveredTimestamp),
                    } as IncomeRow;
                })
                .filter((row) => typeof row.billingEstimateThb === "number");
            setRows(nextRows);
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        void loadData();
    }, []);

    const totalIncome = useMemo(
        () => rows.reduce((sum, row) => sum + (row.billingEstimateThb ?? 0), 0),
        [rows]
    );
    const avgIncome = rows.length ? totalIncome / rows.length : 0;

    if (loading) {
        return (
            <div className="flex h-64 items-center justify-center">
                <Loader2 className="h-8 w-8 animate-spin text-primary" />
            </div>
        );
    }

    return (
        <div className="container mx-auto p-6 space-y-6 max-w-[1600px]">
            <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("accounting.income.title")}</h1>
                    <p className="text-muted-foreground mt-1">{t("accounting.income.subtitle")}</p>
                </div>
                <Button variant="outline" onClick={() => void loadData()}>
                    <RefreshCw className="h-4 w-4 mr-2" />
                    {t("common.refresh")}
                </Button>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.income.stats.totalIncome")}</CardTitle>
                        <DollarSign className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">฿{Math.round(totalIncome).toLocaleString()}</div>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.income.stats.tripCount")}</CardTitle>
                        <Hash className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">{rows.length.toLocaleString()}</div>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.income.stats.avgIncome")}</CardTitle>
                        <TrendingUp className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">฿{Math.round(avgIncome).toLocaleString()}</div>
                    </CardContent>
                </Card>
            </div>

            <Card>
                <CardContent className="pt-6">
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.income.table.tripId")}</TableHead>
                                <TableHead>{t("accounting.income.table.customerId")}</TableHead>
                                <TableHead className="text-right">{t("accounting.income.table.baseRate")}</TableHead>
                                <TableHead className="text-right">{t("accounting.income.table.finalRate")}</TableHead>
                                <TableHead>{t("accounting.income.table.deliveredAt")}</TableHead>
                                <TableHead>{t("accounting.income.table.rateImportId")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {rows.map((row) => (
                                <TableRow key={row.id}>
                                    <TableCell className="font-mono text-xs">{row.spxTripId || row.id}</TableCell>
                                    <TableCell>{row.customerId || "-"}</TableCell>
                                    <TableCell className="text-right">
                                        {row.billingBaseRateThb != null
                                            ? `฿${row.billingBaseRateThb.toLocaleString()}`
                                            : "-"}
                                    </TableCell>
                                    <TableCell className="text-right font-semibold">
                                        {row.billingEstimateThb != null
                                            ? `฿${row.billingEstimateThb.toLocaleString()}`
                                            : "-"}
                                    </TableCell>
                                    <TableCell>
                                        {row.deliveredTimestamp
                                            ? format(row.deliveredTimestamp, "dd/MM/yyyy HH:mm")
                                            : "-"}
                                    </TableCell>
                                    <TableCell className="font-mono text-xs">
                                        {row.billingRateImportId || "-"}
                                    </TableCell>
                                </TableRow>
                            ))}
                            {rows.length === 0 && (
                                <TableRow>
                                    <TableCell colSpan={6} className="h-24 text-center text-muted-foreground">
                                        {t("accounting.income.noRecords")}
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
