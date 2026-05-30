"use client";

import dynamic from "next/dynamic";

const DashboardVehicleMapClient = dynamic(
  () => import("./DashboardVehicleMapClient").then((m) => ({ default: m.DashboardVehicleMapClient })),
  {
    ssr: false,
    loading: () => (
      <div className="rounded-lg border border-border bg-card overflow-hidden">
        <div className="px-3 py-2 border-b border-border">
          <span className="text-sm font-semibold text-foreground">ตำแหน่งรถบนแผนที่</span>
        </div>
        <div className="h-[320px] flex items-center justify-center bg-muted/30">
          <p className="text-sm text-muted-foreground">Loading map...</p>
        </div>
      </div>
    ),
  }
);

export function DashboardVehicleMap() {
  return <DashboardVehicleMapClient />;
}
