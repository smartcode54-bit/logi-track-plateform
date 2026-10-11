"use client";

import { useState, useEffect, useMemo } from "react";
import { collection, query, orderBy, limit, where, getDocs, Timestamp } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import { useLanguage } from "@/context/language";
import { format, subDays } from "date-fns";
import { PagePermissionGuard } from "@/components/page-permission-guard";
import { CAPABILITIES } from "@/lib/capabilities";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Loader2, RefreshCw, TrendingDown, TrendingUp, Minus, Fuel } from "lucide-react";
import {
    Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import {
    Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";

interface FuelPriceItem {
    nameTh: string;
    nameEn: string;
    price: number;
    unit: string;
}

interface DailySnapshot {
    id: string;
    dayKey: string;
    monthKey: string;
    capturedAt: Date | null;
    status: "ok" | "error";
    items: FuelPriceItem[];
}

const DIESEL_KEYWORDS = ["ดีเซล", "diesel", "hi-diesel", "ไฮดีเซล"];
const GASOHOL_KEYWORDS = ["แก๊สโซฮอล", "gasohol", "เบนซิน", "benzin"];

function isDiesel(name: string) {
    return DIESEL_KEYWORDS.some((k) => name.toLowerCase().includes(k.toLowerCase()));
}

function priceColor(diff: number | null) {
    if (diff === null) return "text-muted-foreground";
    if (diff > 0) return "text-red-500";
    if (diff < 0) return "text-green-500";
    return "text-muted-foreground";
}

function PriceDiff({ diff }: { diff: number | null }) {
    if (diff === null) return <span className="text-muted-foreground text-xs">—</span>;
    const Icon = diff > 0 ? TrendingUp : diff < 0 ? TrendingDown : Minus;
    return (
        <span className={`flex items-center gap-0.5 text-xs font-mono ${priceColor(diff)}`}>
            <Icon className="w-3 h-3" />
            {diff > 0 ? "+" : ""}{diff.toFixed(2)}
        </span>
    );
}

const FUEL_TYPES = [
    { value: "all", label: "ทุกประเภท" },
    { value: "diesel", label: "ดีเซล" },
    { value: "gasohol", label: "แก๊สโซฮอล/เบนซิน" },
];

const RANGE_OPTIONS = [
    { value: "30", label: "30 วันล่าสุด" },
    { value: "60", label: "60 วันล่าสุด" },
    { value: "90", label: "90 วันล่าสุด" },
    { value: "180", label: "180 วัน" },
    { value: "365", label: "1 ปี" },
];

export default function FuelPriceHistoryPage() {
    const { t } = useLanguage();
    const [snapshots, setSnapshots] = useState<DailySnapshot[]>([]);
    const [loading, setLoading] = useState(true);
    const [range, setRange] = useState("90");
    const [fuelTypeFilter, setFuelTypeFilter] = useState("diesel");

    const fetchSnapshots = async () => {
        setLoading(true);
        try {
            const fromDate = subDays(new Date(), Number(range));
            const fromKey = format(fromDate, "yyyy-MM-dd");
            const q = query(
                collection(db, COLLECTIONS.FUEL_DAILY_SNAPSHOTS),
                where("dayKey", ">=", fromKey),
                orderBy("dayKey", "desc"),
                limit(400),
            );
            const snap = await getDocs(q);
            setSnapshots(
                snap.docs.map((d) => {
                    const data = d.data();
                    const capturedAt = data.capturedAt instanceof Timestamp
                        ? data.capturedAt.toDate()
                        : data.capturedAt instanceof Date ? data.capturedAt : null;
                    return {
                        id: d.id,
                        dayKey: data.dayKey ?? d.id,
                        monthKey: data.monthKey ?? "",
                        capturedAt,
                        status: data.status ?? "ok",
                        items: (data.items ?? []) as FuelPriceItem[],
                    };
                })
            );
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => { fetchSnapshots(); }, [range]); // eslint-disable-line react-hooks/exhaustive-deps

    // All unique fuel names across all snapshots
    const allFuelNames = useMemo(() => {
        const names = new Set<string>();
        snapshots.forEach((s) => s.items.forEach((i) => names.add(i.nameTh)));
        return Array.from(names).filter((n) => {
            if (fuelTypeFilter === "diesel") return isDiesel(n);
            if (fuelTypeFilter === "gasohol") return GASOHOL_KEYWORDS.some((k) => n.toLowerCase().includes(k.toLowerCase()));
            return true;
        });
    }, [snapshots, fuelTypeFilter]);

    // Build table: rows = snapshot days (newest first), cols = fuel names
    const tableData = useMemo(() => {
        return snapshots.map((snap, idx) => {
            const priceMap = new Map(snap.items.map((i) => [i.nameTh, i.price]));
            const prevSnap = snapshots[idx + 1];
            const prevMap = prevSnap ? new Map(prevSnap.items.map((i) => [i.nameTh, i.price])) : null;
            return {
                snap,
                priceMap,
                diff: (name: string) => {
                    const cur = priceMap.get(name);
                    const prev = prevMap?.get(name);
                    if (cur == null || prev == null) return null;
                    return +(cur - prev).toFixed(2);
                },
            };
        });
    }, [snapshots]);

    // Latest prices for summary cards
    const latestSnap = snapshots[0];
    const prevSnap = snapshots[1];
    const dieselItems = latestSnap?.items.filter((i) => isDiesel(i.nameTh)) ?? [];

    return (
        <PagePermissionGuard capability={CAPABILITIES.accounting_view_fuel}>
            <div className="container py-8 space-y-6">
                <div className="flex items-center justify-between flex-wrap gap-3">
                    <div>
                        <h1 className="text-2xl font-bold flex items-center gap-2">
                            <Fuel className="w-6 h-6 text-amber-500" />
                            ประวัติราคาน้ำมัน
                        </h1>
                        <p className="text-muted-foreground text-sm mt-1">
                            ราคาน้ำมันบางจาก — บันทึกทุกวัน 05:00 น.
                        </p>
                    </div>
                    <Button variant="outline" size="sm" onClick={fetchSnapshots} disabled={loading}>
                        {loading ? <Loader2 className="w-4 h-4 animate-spin" /> : <RefreshCw className="w-4 h-4" />}
                        <span className="ml-1">รีเฟรช</span>
                    </Button>
                </div>

                {/* Summary cards — latest diesel prices */}
                {dieselItems.length > 0 && (
                    <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                        {dieselItems.map((item) => {
                            const prev = prevSnap?.items.find((i) => i.nameTh === item.nameTh);
                            const diff = prev != null ? +(item.price - prev.price).toFixed(2) : null;
                            return (
                                <Card key={item.nameTh} className="border-l-4 border-l-amber-500">
                                    <CardContent className="pt-3 pb-3">
                                        <p className="text-xs text-muted-foreground truncate">{item.nameTh}</p>
                                        <p className="text-xl font-bold font-mono">{item.price.toFixed(2)}</p>
                                        <div className="flex items-center justify-between mt-0.5">
                                            <span className="text-xs text-muted-foreground">{item.unit}</span>
                                            <PriceDiff diff={diff} />
                                        </div>
                                    </CardContent>
                                </Card>
                            );
                        })}
                    </div>
                )}

                {/* Filters */}
                <div className="flex gap-3 flex-wrap">
                    <Select value={range} onValueChange={setRange}>
                        <SelectTrigger className="w-[160px]">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            {RANGE_OPTIONS.map((o) => (
                                <SelectItem key={o.value} value={o.value}>{o.label}</SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <Select value={fuelTypeFilter} onValueChange={setFuelTypeFilter}>
                        <SelectTrigger className="w-[200px]">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            {FUEL_TYPES.map((o) => (
                                <SelectItem key={o.value} value={o.value}>{o.label}</SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                    <span className="text-sm text-muted-foreground self-center">
                        {snapshots.length} วันที่บันทึก
                    </span>
                </div>

                {/* History table */}
                <Card>
                    <CardHeader className="pb-2">
                        <CardTitle className="text-base">ตารางราคารายวัน</CardTitle>
                    </CardHeader>
                    <CardContent className="p-0 overflow-x-auto">
                        {loading ? (
                            <div className="flex justify-center py-12">
                                <Loader2 className="w-6 h-6 animate-spin text-muted-foreground" />
                            </div>
                        ) : snapshots.length === 0 ? (
                            <div className="text-center py-12 text-muted-foreground text-sm">
                                ยังไม่มีข้อมูลประวัติราคา — จะเริ่มเก็บตั้งแต่วันนี้ 05:00 น.
                            </div>
                        ) : (
                            <Table>
                                <TableHeader>
                                    <TableRow>
                                        <TableHead className="sticky left-0 bg-background z-10 min-w-[110px]">วันที่</TableHead>
                                        {allFuelNames.map((name) => (
                                            <TableHead key={name} className="text-right min-w-[110px] text-xs">
                                                {name}
                                            </TableHead>
                                        ))}
                                    </TableRow>
                                </TableHeader>
                                <TableBody>
                                    {tableData.map(({ snap, priceMap, diff }) => (
                                        <TableRow key={snap.id}>
                                            <TableCell className="sticky left-0 bg-background z-10 font-mono text-xs">
                                                <div>{format(new Date(snap.dayKey), "dd/MM/yyyy")}</div>
                                                {snap.capturedAt && (
                                                    <div className="text-muted-foreground text-[10px]">
                                                        {format(snap.capturedAt, "HH:mm")}
                                                    </div>
                                                )}
                                            </TableCell>
                                            {allFuelNames.map((name) => {
                                                const price = priceMap.get(name);
                                                const d = diff(name);
                                                return (
                                                    <TableCell key={name} className="text-right">
                                                        {price != null ? (
                                                            <div>
                                                                <span className="font-mono text-sm">{price.toFixed(2)}</span>
                                                                <div><PriceDiff diff={d} /></div>
                                                            </div>
                                                        ) : (
                                                            <span className="text-muted-foreground">—</span>
                                                        )}
                                                    </TableCell>
                                                );
                                            })}
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                        )}
                    </CardContent>
                </Card>

                {snapshots.length === 0 && !loading && (
                    <Card className="border-amber-500/30 bg-amber-500/5">
                        <CardContent className="pt-4 pb-4 text-sm text-amber-700 dark:text-amber-400">
                            <strong>หมายเหตุ:</strong> ระบบจะเริ่มเก็บ history ตั้งแต่วันนี้เป็นต้นไป
                            หากต้องการข้อมูลย้อนหลัง ให้รัน <strong>Manual Sync</strong> จากหน้า Fuel
                            แล้วระบบจะบันทึก snapshot วันปัจจุบันทันที
                        </CardContent>
                    </Card>
                )}
            </div>
        </PagePermissionGuard>
    );
}
