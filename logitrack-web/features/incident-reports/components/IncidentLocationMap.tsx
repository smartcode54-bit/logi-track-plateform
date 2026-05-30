"use client";

import dynamic from "next/dynamic";
import { Loader2 } from "lucide-react";

interface IncidentLocationMapProps {
    lat: number;
    lng: number;
}

const ClientMap = dynamic(
    () => import("./IncidentLocationMapClient").then((mod) => ({ default: mod.IncidentLocationMapClient })),
    {
        ssr: false,
        loading: () => (
            <div className="w-full h-full flex flex-col items-center justify-center bg-muted/30">
                <Loader2 className="h-5 w-5 animate-spin text-muted-foreground mb-2" />
                <p className="text-xs text-muted-foreground">Loading map...</p>
            </div>
        ),
    }
);

export function IncidentLocationMap(props: IncidentLocationMapProps) {
    return <ClientMap {...props} />;
}
