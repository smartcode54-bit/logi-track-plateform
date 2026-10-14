"use client";

/**
 * The map card of the dashboard over Go (T54R): the header, the loading and error states and the
 * incident legend; the Leaflet part is `FleetMapClient`, loaded with `next/dynamic` only when the card
 * renders a map, so leaflet is never in the route's initial JS (developer-spec.md §10.11).
 */
import dynamic from "next/dynamic";
import { RefreshCw } from "lucide-react";

import { useLanguage } from "@/context/language";
import type { VehicleLocationDTO } from "../api/vehicleLocationEvents";

const FleetMapClient = dynamic(() => import("./FleetMapClient").then((m) => m.FleetMapClient), {
    ssr: false,
    loading: () => <div className="h-[500px] min-h-[400px] w-full bg-muted/30" />,
});

export interface FleetMapCardProps {
    vehicles: readonly VehicleLocationDTO[];
    loading: boolean;
    error: boolean;
    onRetry: () => void;
    incidentTruckIds: ReadonlySet<string>;
    incidentPlates: ReadonlySet<string>;
    /** Rows of the loaded incident page, or undefined while it is not shown. */
    incidentCount?: number;
}

export function FleetMap({ vehicles, loading, error, onRetry, incidentTruckIds, incidentPlates, incidentCount }: FleetMapCardProps) {
    const { t } = useLanguage();
    return (
        <div className="rounded-lg border border-border bg-card overflow-hidden" data-testid="fleet-map">
            <div className="px-3 py-2 border-b border-border">
                <h2 className="text-sm font-semibold text-foreground">{t("dashboard.vehicleMap.title")}</h2>
                {incidentCount !== undefined && incidentCount > 0 && (
                    <p className="text-xs text-muted-foreground">{t("dashboard.vehicleMap.incidentLegend", { count: incidentCount })}</p>
                )}
            </div>
            {loading ? (
                <div className="h-[500px] min-h-[400px] flex items-center justify-center bg-muted/30">
                    <p className="text-sm text-muted-foreground">{t("dashboard.vehicleMap.loading")}</p>
                </div>
            ) : error ? (
                <div className="h-[200px] flex flex-col items-center justify-center gap-2 bg-muted/30">
                    <p className="text-sm text-muted-foreground">{t("dashboard.vehiclePosition.loadError")}</p>
                    <button type="button" onClick={onRetry} className="inline-flex items-center gap-1.5 text-xs font-medium text-primary hover:underline">
                        <RefreshCw className="h-3.5 w-3.5" />
                        {t("dashboard.vehiclePosition.retry")}
                    </button>
                </div>
            ) : (
                <FleetMapClient vehicles={vehicles} incidentTruckIds={incidentTruckIds} incidentPlates={incidentPlates} />
            )}
        </div>
    );
}
