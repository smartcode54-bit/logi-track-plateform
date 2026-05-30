"use client";

import { useEffect, useMemo, useState } from "react";
import { MapContainer, TileLayer, Marker } from "react-leaflet";
import L from "leaflet";
import "leaflet/dist/leaflet.css";

const markerIcon = L.icon({
    iconUrl: "https://unpkg.com/leaflet@1.9.4/dist/images/marker-icon.png",
    shadowUrl: "https://unpkg.com/leaflet@1.9.4/dist/images/marker-shadow.png",
    iconSize: [25, 41],
    iconAnchor: [12, 41],
});

type Props = {
    lat: number;
    lng: number;
};

export default function SessionLoginLocationMap({ lat, lng }: Props) {
    const center = useMemo(() => ({ lat, lng }), [lat, lng]);
    const [mapId] = useState(() => `session-login-map-${Math.random().toString(36).slice(2, 11)}`);

    useEffect(() => {
        return () => {
            const container = L.DomUtil.get(mapId);
            if (container) {
                // eslint-disable-next-line @typescript-eslint/no-explicit-any
                (container as any)._leaflet_id = null;
            }
        };
    }, [mapId]);

    return (
        <div className="h-[280px] w-full rounded-md overflow-hidden border border-border">
            <MapContainer
                id={mapId}
                key={`${mapId}-${lat}-${lng}`}
                center={center}
                zoom={11}
                scrollWheelZoom
                style={{ height: "100%", width: "100%" }}
            >
                <TileLayer
                    attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>'
                    url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png"
                />
                <Marker position={center} icon={markerIcon} />
            </MapContainer>
        </div>
    );
}
