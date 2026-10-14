"use client";

import { AlertTriangle, ArrowRight, Camera } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useLanguage } from "@/context/language";
import { formatDateTime } from "@/features/accounting/components/go/shared";
import { cn } from "@/lib/utils";
import { deliveredTimeOf, placeLabel, type MonitorRowDTO } from "../../api/monitor.go";
import { STATUS_COLOR } from "./monitorStyles";

/** A measured cell in the monitor's single virtual table, using the aggregate DTO without another request. */
export function MonitorTripSummary({ row, onOpen }: { row: MonitorRowDTO; onOpen: (row: MonitorRowDTO) => void }) {
    const { t } = useLanguage();
    const delivered = deliveredTimeOf(row);
    const timestamp = delivered ?? row.checkInAt ?? row.createdAt;
    const timeLabel = delivered ? "driverMonitor.table.deliveredTime" : row.checkInAt ? "driverMonitor.table.checkInAt" : "driverMonitor.table.depart";
    const destination = row.stops?.length ? placeLabel(row.stops[row.stops.length - 1].place) : placeLabel(row.destination);

    return (
        <div className="min-w-0 space-y-2 py-1">
            <div className="flex flex-wrap items-start justify-between gap-2">
                <span className="min-w-0 break-all font-mono font-medium">{row.tripNo}</span>
                <span className="flex shrink-0 items-center gap-1">
                    <Badge variant="outline" className={cn("shrink-0", STATUS_COLOR[row.status])}>
                        {t(`driverMonitor.status.${row.status}`)}
                    </Badge>
                    {row.incidentCount > 0 && row.status !== "incident" ? <AlertTriangle className="size-4 text-destructive" aria-label={t("driverMonitor.status.incident")} /> : null}
                </span>
            </div>
            <div className="flex min-w-0 items-start gap-2">
                <span className="line-clamp-2 min-w-0 flex-1 break-words"><span className="sr-only">{t("driverMonitor.table.origin")}: </span>{placeLabel(row.origin)}</span>
                <ArrowRight className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
                <span className="line-clamp-2 min-w-0 flex-1 break-words"><span className="sr-only">{t("driverMonitor.table.destination")}: </span>{destination}</span>
            </div>
            {row.stops?.length > 0 ? <p className="text-xs text-muted-foreground">{t("taskBoard.stops", { count: row.stops.length })}</p> : null}
            <p className="line-clamp-2 break-words text-muted-foreground">
                <span className="font-mono">{row.truck?.plate ?? "—"}</span>{" · "}{row.driverName ?? t("driverMonitor.table.unknown")}
            </p>
            <Button variant="outline" className="min-h-11 w-full min-w-11 whitespace-normal break-words text-left" onClick={() => onOpen(row)}>
                {t("driverMonitor.openTrip", { tripNo: row.tripNo })}
            </Button>
            <p className="text-xs tabular-nums text-muted-foreground">{t(timeLabel)}: {formatDateTime(timestamp)}</p>
            <p className="flex items-center gap-1 text-xs text-muted-foreground">
                <Camera className="size-4 shrink-0" aria-hidden />{t("driverMonitor.photoCount", { count: row.photoCount })}
            </p>
        </div>
    );
}
