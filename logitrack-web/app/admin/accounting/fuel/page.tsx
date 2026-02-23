"use client";

import { useEffect, useState, useMemo } from "react";
import { useLanguage } from "@/context/language";
import {
    getVehicleExpensesByType,
    getDriversForFilter,
    getTrucksForFilter,
    VehicleExpenseRow,
    DriverOption,
    TruckOption,
} from "../actions.client";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Fuel, DollarSign, Hash, TrendingUp, Loader2, Gauge, Search, RefreshCw } from "lucide-react";
import { ImagePreviewGallery } from "@/components/accounting/ImagePreviewGallery";
import { format } from "date-fns";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";

export interface FuelRow extends VehicleExpenseRow {
    kmPerLiter?: number;
    /** ระยะทาง (กม.) ตั้งแต่การเติมครั้งก่อน */
    distanceKm?: number;
}

function computeKmPerLiter(records: VehicleExpenseRow[]): FuelRow[] {
    const byDriver = new Map<string, VehicleExpenseRow[]>();
    records.forEach((r) => {
        const key = r.truckId ?? r.driverId;
        if (!byDriver.has(key)) byDriver.set(key, []);
        byDriver.get(key)!.push(r);
    });
    const result: FuelRow[] = [];
    byDriver.forEach((rows) => {
        const sorted = [...rows].sort((a, b) => a.date.getTime() - b.date.getTime());
        sorted.forEach((row, i) => {
            const prev = i > 0 ? sorted[i - 1] : null;
            const prevOdo = prev?.odometer;
            const currOdo = row.odometer;
            const vol = row.volumeLiters;
            let kmPerLiter: number | undefined;
            let distanceKm: number | undefined;
            if (
                prevOdo != null &&
                currOdo != null &&
                currOdo >= prevOdo
            ) {
                distanceKm = currOdo - prevOdo;
                if (vol != null && vol > 0) {
                    kmPerLiter = Math.round((distanceKm / vol) * 10) / 10;
                }
            }
            result.push({ ...row, kmPerLiter, distanceKm });
        });
    });
    return result.sort((a, b) => b.date.getTime() - a.date.getTime());
}

export default function AccountingFuelPage() {
    const { t } = useLanguage();
    const [records, setRecords] = useState<VehicleExpenseRow[]>([]);
    const [drivers, setDrivers] = useState<DriverOption[]>([]);
    const [trucks, setTrucks] = useState<TruckOption[]>([]);
    const [loading, setLoading] = useState(true);
    const [filterDriverId, setFilterDriverId] = useState<string>("all");
    const [filterTruckId, setFilterTruckId] = useState<string>("all");
    const [plateSearch, setPlateSearch] = useState("");
    const [filterKmOp, setFilterKmOp] = useState<string>("");
    const [filterKmValue, setFilterKmValue] = useState<string>("");
    const [filterKmMin, setFilterKmMin] = useState<string>("");
    const [filterKmMax, setFilterKmMax] = useState<string>("");
    const [detailRow, setDetailRow] = useState<FuelRow | null>(null);

    const loadData = () => {
        setLoading(true);
        Promise.all([
            getVehicleExpensesByType("fuel"),
            getDriversForFilter(),
            getTrucksForFilter(),
        ]).then(([rows, driverList, truckList]) => {
            setRecords(rows);
            setDrivers(driverList);
            setTrucks(truckList);
        }).finally(() => setLoading(false));
    };

    useEffect(() => {
        loadData();
    }, []);

    const rowsWithKm = useMemo(() => computeKmPerLiter(records), [records]);

    const filteredRecords = useMemo(() => {
        let list = rowsWithKm;
        if (filterDriverId !== "all") list = list.filter((r) => r.driverId === filterDriverId);
        if (filterTruckId !== "all") list = list.filter((r) => r.truckId === filterTruckId);
        if (plateSearch.trim()) {
            const q = plateSearch.trim().toLowerCase();
            list = list.filter(
                (r) =>
                    (r.licensePlate ?? "").toLowerCase().includes(q) ||
                    (r.driverName ?? "").toLowerCase().includes(q)
            );
        }
        if (filterKmOp && filterKmOp !== "between") {
            const val = parseFloat(filterKmValue);
            if (filterKmValue.trim() !== "" && !Number.isNaN(val)) {
                list = list.filter((r) => {
                    const km = (r as FuelRow).kmPerLiter;
                    if (km == null) return false;
                    if (filterKmOp === "=") return Math.abs(km - val) < 0.01;
                    if (filterKmOp === "<") return km < val;
                    if (filterKmOp === "<=") return km <= val;
                    if (filterKmOp === ">") return km > val;
                    if (filterKmOp === ">=") return km >= val;
                    return true;
                });
            }
        } else if (filterKmOp === "between") {
            const min = filterKmMin.trim() !== "" ? parseFloat(filterKmMin) : null;
            const max = filterKmMax.trim() !== "" ? parseFloat(filterKmMax) : null;
            if (min != null && !Number.isNaN(min)) {
                list = list.filter((r) => (r as FuelRow).kmPerLiter != null && (r as FuelRow).kmPerLiter! >= min);
            }
            if (max != null && !Number.isNaN(max)) {
                list = list.filter((r) => (r as FuelRow).kmPerLiter != null && (r as FuelRow).kmPerLiter! <= max);
            }
        }
        return list;
    }, [rowsWithKm, filterDriverId, filterTruckId, plateSearch, filterKmOp, filterKmValue, filterKmMin, filterKmMax]);

    const totalAmount = filteredRecords.reduce((s, r) => s + r.amount, 0);
    const count = filteredRecords.length;
    const avgAmount = count > 0 ? totalAmount / count : 0;
    const now = new Date();
    const thisMonth = filteredRecords.filter(
        (r) => r.date.getMonth() === now.getMonth() && r.date.getFullYear() === now.getFullYear()
    );
    const thisMonthTotal = thisMonth.reduce((s, r) => s + r.amount, 0);
    const kmPerLiterValues = filteredRecords
        .map((r) => (r as FuelRow).kmPerLiter)
        .filter((v): v is number => v != null && v > 0);
    const avgKmPerLiter =
        kmPerLiterValues.length > 0
            ? Math.round((kmPerLiterValues.reduce((a, b) => a + b, 0) / kmPerLiterValues.length) * 10) / 10
            : null;

    if (loading) {
        return (
            <div className="flex h-64 items-center justify-center">
                <Loader2 className="h-8 w-8 animate-spin text-primary" />
            </div>
        );
    }

    return (
        <div className="container mx-auto p-6 space-y-8 max-w-[1600px]">
            <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
                <div>
                    <h1 className="text-3xl font-bold tracking-tight">{t("accounting.fuel.title")}</h1>
                    <p className="text-muted-foreground mt-1">{t("accounting.fuel.subtitle")}</p>
                </div>
            </div>

            {/* Filters */}
            <Card>
                <CardHeader className="pb-3">
                    <CardTitle className="text-base flex items-center gap-2">
                        <Search className="h-4 w-4" />
                        {t("accounting.filter.title")}
                    </CardTitle>
                </CardHeader>
                <CardContent className="flex flex-wrap gap-4">
                    <div className="flex flex-col gap-1.5">
                        <label className="text-xs font-medium text-muted-foreground">
                            {t("accounting.filter.driver")}
                        </label>
                        <Select value={filterDriverId} onValueChange={setFilterDriverId}>
                            <SelectTrigger className="w-[200px]">
                                <SelectValue placeholder={t("accounting.filter.all")} />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("accounting.filter.all")}</SelectItem>
                                {drivers.map((d) => (
                                    <SelectItem key={d.id} value={d.id}>
                                        {d.name}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="flex flex-col gap-1.5">
                        <label className="text-xs font-medium text-muted-foreground">
                            {t("accounting.filter.vehicle")}
                        </label>
                        <Select value={filterTruckId} onValueChange={setFilterTruckId}>
                            <SelectTrigger className="w-[180px]">
                                <SelectValue placeholder={t("accounting.filter.all")} />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("accounting.filter.all")}</SelectItem>
                                {trucks.map((truck) => (
                                    <SelectItem key={truck.id} value={truck.id}>
                                        {truck.licensePlate}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="flex flex-col gap-1.5">
                        <label className="text-xs font-medium text-muted-foreground">
                            {t("accounting.filter.title")}
                        </label>
                        <Input
                            placeholder={t("accounting.filter.searchPlaceholder")}
                            className="w-[180px]"
                            value={plateSearch}
                            onChange={(e) => setPlateSearch(e.target.value)}
                        />
                    </div>
                    <div className="flex flex-col gap-1.5">
                        <label className="text-xs font-medium text-muted-foreground">
                            {t("accounting.filter.kmPerLiter")}
                        </label>
                        <div className="flex flex-wrap items-end gap-2">
                            <Select value={filterKmOp || "none"} onValueChange={(v) => setFilterKmOp(v === "none" ? "" : v)}>
                                <SelectTrigger className="w-[110px]">
                                    <SelectValue placeholder={t("accounting.filter.all")} />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="none">{t("accounting.filter.all")}</SelectItem>
                                    <SelectItem value="=">{t("accounting.filter.kmOp.eq")}</SelectItem>
                                    <SelectItem value="<">{t("accounting.filter.kmOp.lt")}</SelectItem>
                                    <SelectItem value="<=">{t("accounting.filter.kmOp.lte")}</SelectItem>
                                    <SelectItem value=">">{t("accounting.filter.kmOp.gt")}</SelectItem>
                                    <SelectItem value=">=">{t("accounting.filter.kmOp.gte")}</SelectItem>
                                    <SelectItem value="between">{t("accounting.filter.kmOp.between")}</SelectItem>
                                </SelectContent>
                            </Select>
                            {filterKmOp && filterKmOp !== "between" && (
                                <Input
                                    type="number"
                                    inputMode="decimal"
                                    min={0}
                                    step={0.1}
                                    placeholder="0"
                                    className="w-[90px]"
                                    value={filterKmValue}
                                    onChange={(e) => setFilterKmValue(e.target.value)}
                                />
                            )}
                            {filterKmOp === "between" && (
                                <>
                                    <Input
                                        type="number"
                                        inputMode="decimal"
                                        min={0}
                                        step={0.1}
                                        placeholder="Min"
                                        className="w-[80px]"
                                        value={filterKmMin}
                                        onChange={(e) => setFilterKmMin(e.target.value)}
                                    />
                                    <span className="text-muted-foreground text-sm">–</span>
                                    <Input
                                        type="number"
                                        inputMode="decimal"
                                        min={0}
                                        step={0.1}
                                        placeholder="Max"
                                        className="w-[80px]"
                                        value={filterKmMax}
                                        onChange={(e) => setFilterKmMax(e.target.value)}
                                    />
                                </>
                            )}
                        </div>
                    </div>
                </CardContent>
            </Card>

            {/* Mini dashboard */}
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-5 gap-4">
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.dashboard.totalAmount")}</CardTitle>
                        <DollarSign className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">฿{totalAmount.toLocaleString()}</div>
                        <p className="text-xs text-muted-foreground">{t("accounting.dashboard.recordCount")}: {count}</p>
                    </CardContent>
                </Card>
                <Card className="border-l-4 border-l-blue-500">
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-blue-700">{t("accounting.dashboard.thisMonth")}</CardTitle>
                        <TrendingUp className="h-4 w-4 text-blue-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-blue-700">฿{thisMonthTotal.toLocaleString()}</div>
                        <p className="text-xs text-muted-foreground">{thisMonth.length} {t("accounting.dashboard.recordCount").toLowerCase()}</p>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.dashboard.avgAmount")}</CardTitle>
                        <Hash className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">฿{Math.round(avgAmount).toLocaleString()}</div>
                    </CardContent>
                </Card>
                <Card className="border-l-4 border-l-green-500">
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium text-green-700">{t("accounting.dashboard.avgKmPerLiter")}</CardTitle>
                        <Gauge className="h-4 w-4 text-green-500" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold text-green-700">
                            {avgKmPerLiter != null ? `${avgKmPerLiter} km/L` : "—"}
                        </div>
                        <p className="text-xs text-muted-foreground">
                            {kmPerLiterValues.length} {t("accounting.dashboard.recordCount").toLowerCase()}
                        </p>
                    </CardContent>
                </Card>
                <Card>
                    <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <CardTitle className="text-sm font-medium">{t("accounting.dashboard.recordCount")}</CardTitle>
                        <Fuel className="h-4 w-4 text-muted-foreground" />
                    </CardHeader>
                    <CardContent>
                        <div className="text-2xl font-bold">{count}</div>
                    </CardContent>
                </Card>
            </div>

            {/* Table */}
            <Card>
                <CardHeader className="flex flex-row items-center justify-between space-y-0">
                    <CardTitle>{t("accounting.fuel.title")}</CardTitle>
                    <Button variant="outline" size="sm" onClick={loadData} disabled={loading}>
                        <RefreshCw className={`h-4 w-4 mr-2 ${loading ? "animate-spin" : ""}`} />
                        {t("common.refresh")}
                    </Button>
                </CardHeader>
                <CardContent>
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead>{t("accounting.table.date")}</TableHead>
                                <TableHead>{t("accounting.table.driver")}</TableHead>
                                <TableHead>{t("accounting.filter.vehicle")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.amount")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.volume")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.pricePerLiter")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.odometer")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.distanceKm")}</TableHead>
                                <TableHead className="text-right">{t("accounting.table.kmPerLiter")}</TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {filteredRecords.map((row) => (
                                <TableRow
                                    key={row.id}
                                    className="cursor-pointer hover:bg-muted/50 transition-colors"
                                    onClick={() => setDetailRow(row as FuelRow)}
                                >
                                    <TableCell className="whitespace-nowrap font-mono text-xs">
                                        {format(row.date, "dd MMM yyyy")}
                                    </TableCell>
                                    <TableCell>{row.driverName ?? (row.driverId || "—")}</TableCell>
                                    <TableCell className="font-mono text-xs">{row.licensePlate ?? "—"}</TableCell>
                                    <TableCell className="text-right font-semibold">฿{row.amount.toLocaleString()}</TableCell>
                                    <TableCell className="text-right text-muted-foreground">
                                        {row.volumeLiters != null ? row.volumeLiters.toLocaleString() : "—"}
                                    </TableCell>
                                    <TableCell className="text-right text-muted-foreground">
                                        {row.pricePerLiter != null ? `฿${row.pricePerLiter.toLocaleString()}` : "—"}
                                    </TableCell>
                                    <TableCell className="text-right text-muted-foreground">
                                        {row.odometer != null ? row.odometer.toLocaleString() : "—"}
                                    </TableCell>
                                    <TableCell className="text-right text-muted-foreground">
                                        {(row as FuelRow).distanceKm != null
                                            ? (row as FuelRow).distanceKm!.toLocaleString()
                                            : "—"}
                                    </TableCell>
                                    <TableCell className="text-right font-medium">
                                        {(row as FuelRow).kmPerLiter != null
                                            ? `${(row as FuelRow).kmPerLiter}`
                                            : "—"}
                                    </TableCell>
                                </TableRow>
                            ))}
                            {filteredRecords.length === 0 && (
                                <TableRow>
                                    <TableCell colSpan={9} className="h-24 text-center text-muted-foreground">
                                        {t("accounting.noRecords")}
                                    </TableCell>
                                </TableRow>
                            )}
                        </TableBody>
                    </Table>
                </CardContent>
            </Card>

            {/* Detail Dialog */}
            <Dialog open={!!detailRow} onOpenChange={(open) => !open && setDetailRow(null)}>
                <DialogContent className="max-w-2xl max-h-[90vh] overflow-hidden flex flex-col p-4">
                    <DialogHeader className="shrink-0">
                        <DialogTitle>{t("accounting.detail.title")}</DialogTitle>
                        <DialogDescription>
                            {detailRow && format(detailRow.date, "dd MMM yyyy")}
                        </DialogDescription>
                    </DialogHeader>
                    {detailRow && (
                        <div className="flex flex-col gap-4 min-h-0 flex-1 overflow-hidden py-2">
                            <div className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm bg-muted/30 rounded-lg p-3 shrink-0">
                                <span className="text-muted-foreground">{t("accounting.table.date")}</span>
                                <span className="font-medium">{format(detailRow.date, "dd MMM yyyy")}</span>
                                <span className="text-muted-foreground">{t("accounting.detail.driver")}</span>
                                <span className="font-medium">{detailRow.driverName ?? (detailRow.driverId || "—")}</span>
                                <span className="text-muted-foreground">{t("accounting.detail.vehicle")}</span>
                                <span className="font-mono">{detailRow.licensePlate ?? "—"}</span>
                                <span className="text-muted-foreground">{t("accounting.detail.amount")}</span>
                                <span className="font-semibold">฿{detailRow.amount.toLocaleString()}</span>
                                <span className="text-muted-foreground">{t("accounting.detail.volume")}</span>
                                <span>{detailRow.volumeLiters != null ? detailRow.volumeLiters.toLocaleString() : "—"}</span>
                                <span className="text-muted-foreground">{t("accounting.detail.pricePerLiter")}</span>
                                <span>{detailRow.pricePerLiter != null ? `฿${detailRow.pricePerLiter.toLocaleString()}` : "—"}</span>
                                <span className="text-muted-foreground">{t("accounting.detail.odometer")}</span>
                                <span>{detailRow.odometer != null ? detailRow.odometer.toLocaleString() : "—"}</span>
                                <span className="text-muted-foreground">{t("accounting.detail.distanceKm")}</span>
                                <span>{detailRow.distanceKm != null ? detailRow.distanceKm.toLocaleString() : "—"}</span>
                                <span className="text-muted-foreground">{t("accounting.detail.kmPerLiter")}</span>
                                <span className="font-medium">
                                    {detailRow.kmPerLiter != null ? `${detailRow.kmPerLiter} km/L` : "—"}
                                </span>
                            </div>
                            {(detailRow.receiptPhotoUrl || detailRow.odometerPhotoUrl) && (
                                <div className="min-h-0 flex-1 flex flex-col max-h-[42vh]">
                                    <ImagePreviewGallery
                                        items={[
                                            ...(detailRow.receiptPhotoUrl
                                                ? [{ url: detailRow.receiptPhotoUrl!, label: t("accounting.detail.receiptPhoto") }]
                                                : []),
                                            ...(detailRow.odometerPhotoUrl
                                                ? [{ url: detailRow.odometerPhotoUrl!, label: t("accounting.detail.odometerPhoto") }]
                                                : []),
                                        ]}
                                        compact
                                    />
                                </div>
                            )}
                            <DialogFooter className="shrink-0">
                                <Button variant="outline" onClick={() => setDetailRow(null)}>
                                    {t("accounting.detail.close")}
                                </Button>
                            </DialogFooter>
                        </div>
                    )}
                </DialogContent>
            </Dialog>
        </div>
    );
}
