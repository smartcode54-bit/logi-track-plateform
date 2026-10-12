"use client";

/**
 * The monitor's export over Go (T34; AC "Excel export pages through `GET /v1/trips/monitor` with its cursor and a
 * lazy xlsx"): the filters on screen, every page of the route (`facets=false`, 200 per page) until the cursor ends,
 * then the sheet (`buildMonitorExport`) as Excel (xlsx loaded on the click) or CSV. No window, no cap: the file
 * holds every trip the filters match. `total` is shown up front only when the page knows that count (no filter on:
 * the range's facets total; filters on: every page loaded); otherwise the progress line counts the trips streamed.
 */
import { useRef, useState } from "react";
import { Loader2 } from "lucide-react";

import { useLanguage } from "@/context/language";
import { formatDateTime } from "@/features/accounting/components/go/shared";
import { apiErrorText } from "@/lib/apiError";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";

import { buildMonitorExport, fetchMonitorRows, toCsv, type MonitorFilter } from "../../api/monitor.go";

export function MonitorExportDialog({ filter, total, onClose }: { filter: MonitorFilter; total: number | null; onClose: () => void }) {
    const { t } = useLanguage();
    const [busy, setBusy] = useState<"xlsx" | "csv" | null>(null);
    const [loaded, setLoaded] = useState(0);
    const [error, setError] = useState<string | null>(null);
    const abort = useRef<AbortController | null>(null);

    const sheet = async () => {
        abort.current = new AbortController();
        const rows = await fetchMonitorRows(filter, abort.current.signal, setLoaded);
        return buildMonitorExport(rows, filter.destination, {
            headers: [
                t("driverMonitor.table.tripId"),
                t("driverMonitor.table.checkInAt"),
                t("driverMonitor.table.driver"),
                t("driverMonitor.table.licensePlate"),
                t("driverMonitor.table.jobType"),
                t("driverMonitor.table.origin"),
                t("driverMonitor.table.destination"),
                t("driverMonitor.table.sealCode"),
                t("driverMonitor.table.partnerCode"),
                t("driverMonitor.table.status"),
                t("driverMonitor.table.estimatedRevenue"),
                t("driverMonitor.table.multiDrop"),
                t("driverMonitor.table.stopNumber"),
                t("driverMonitor.table.stopStatus"),
                t("driverMonitor.table.stopDeliveredTime"),
            ],
            status: (s) => t(`driverMonitor.status.${s}`),
            stopStatus: (s) => (s === "delivered" ? t("driverMonitor.status.delivered") : t(`taskBoard.stopStatus.${s}`, s)),
            jobType: (j) => (j === "line_haul" ? t("driverMonitor.jobType.lineHaul") : t("driverMonitor.jobType.firstMile")),
            yes: t("driverMonitor.table.multiDropYes"),
            no: t("driverMonitor.table.multiDropNo"),
            formatDateTime: (iso) => (iso ? formatDateTime(iso) : ""),
        });
    };

    const run = async (kind: "xlsx" | "csv") => {
        setBusy(kind);
        setError(null);
        setLoaded(0);
        try {
            const data = await sheet();
            const name = `driver_monitor_${filter.from}_${filter.to}`;
            if (kind === "xlsx") {
                // xlsx loads on the export, never with the page (developer-spec.md §10.11).
                const XLSX = await import("xlsx");
                const wb = XLSX.utils.book_new();
                XLSX.utils.book_append_sheet(wb, XLSX.utils.aoa_to_sheet(data), "Trips");
                XLSX.writeFile(wb, `${name}.xlsx`);
            } else {
                const url = URL.createObjectURL(new Blob([toCsv(data)], { type: "text/csv;charset=utf-8" }));
                const a = document.createElement("a");
                a.href = url;
                a.download = `${name}.csv`;
                a.click();
                URL.revokeObjectURL(url);
            }
            onClose();
        } catch (e) {
            if ((e as Error)?.name !== "AbortError") setError(apiErrorText(e, t));
        } finally {
            setBusy(null);
        }
    };

    return (
        <Dialog
            open
            onOpenChange={(o) => {
                if (o) return;
                abort.current?.abort();
                onClose();
            }}
        >
            <DialogContent className="sm:max-w-md">
                <DialogHeader>
                    <DialogTitle>{t("driverMonitor.export.title")}</DialogTitle>
                    <DialogDescription>{t("driverMonitor.export.description")}</DialogDescription>
                </DialogHeader>
                <div className="space-y-2 text-sm">
                    <p>
                        {t("driverMonitor.export.dateRange")}: {filter.from} – {filter.to}
                    </p>
                    {total != null ? <p>{t("taskBoard.export.rowCount", { count: total })}</p> : null}
                    {busy ? (
                        <p className="flex items-center gap-2 text-muted-foreground">
                            <Loader2 className="h-4 w-4 animate-spin" />
                            {t("taskBoard.export.progress", { count: loaded })}
                        </p>
                    ) : null}
                    {error ? <p className="text-destructive">{error}</p> : null}
                </div>
                <DialogFooter className="gap-2">
                    <Button variant="outline" onClick={() => { abort.current?.abort(); onClose(); }}>
                        {t("driverMonitor.export.cancel")}
                    </Button>
                    <Button variant="secondary" disabled={busy !== null} onClick={() => void run("csv")}>
                        {busy === "csv" ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("driverMonitor.export.csv")}
                    </Button>
                    <Button disabled={busy !== null} onClick={() => void run("xlsx")}>
                        {busy === "xlsx" ? <Loader2 className="mr-2 h-4 w-4 animate-spin" /> : null}
                        {t("driverMonitor.export.excel")}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
