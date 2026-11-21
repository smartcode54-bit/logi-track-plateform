"use client";

import { AdvancedMarker, AdvancedMarkerAnchorPoint } from "@vis.gl/react-google-maps";

import { GoogleMap, PanTo } from "@/components/map/GoogleMap";

interface IncidentLocationMapClientProps {
    lat: number;
    lng: number;
}

export function IncidentLocationMapClient({ lat, lng }: IncidentLocationMapClientProps) {
    const position = { lat, lng };
    return (
        // No zoom control: a cleaner look in a small dialog.
        <GoogleMap defaultCenter={position} defaultZoom={15} zoomControl={false} className="z-0">
            <PanTo position={position} />
            <AdvancedMarker position={position} anchorPoint={AdvancedMarkerAnchorPoint.CENTER}>
                <div
                    className="incident-marker"
                    style={{ background: "var(--destructive)", borderRadius: "50%", width: 32, height: 32, display: "flex", alignItems: "center", justifyContent: "center", border: "2px solid var(--card)", overflow: "hidden" }}
                >
                    {/* eslint-disable-next-line @next/next/no-img-element -- a 20 px marker glyph inside the map */}
                    <img src="/exclamation_8848378.png" alt="" style={{ width: 20, height: 20, objectFit: "contain" }} />
                </div>
            </AdvancedMarker>
        </GoogleMap>
    );
}
