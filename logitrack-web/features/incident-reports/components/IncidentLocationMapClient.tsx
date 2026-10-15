"use client";

import { useEffect, useRef } from "react";
import "leaflet/dist/leaflet.css";
import L from "leaflet";

interface IncidentLocationMapClientProps {
    lat: number;
    lng: number;
}

export function IncidentLocationMapClient({ lat, lng }: IncidentLocationMapClientProps) {
    const mapContainerRef = useRef<HTMLDivElement>(null);
    const mapInstanceRef = useRef<L.Map | null>(null);
    const markerRef = useRef<L.Marker | null>(null);

    useEffect(() => {
        if (typeof window === "undefined" || !mapContainerRef.current) return;

        if (!mapInstanceRef.current) {
            mapInstanceRef.current = L.map(mapContainerRef.current, {
                zoomControl: false // Disable zoom control to make it look cleaner in a small dialog
            }).setView([lat, lng], 15);

            L.tileLayer("https://{s}.basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}{r}.png", {
                maxZoom: 19,
                attribution: '&copy; <a href="https://carto.com/">CARTO</a>',
            }).addTo(mapInstanceRef.current);

            const icon = L.divIcon({
                className: "incident-marker",
                html: `
          <div style="background:var(--destructive);border-radius:50%;width:32px;height:32px;display:flex;align-items:center;justify-content:center;border:2px solid var(--card);overflow:hidden;">
            <img src="/exclamation_8848378.png" alt="" style="width:20px;height:20px;object-fit:contain;" />
          </div>
        `,
                iconSize: [32, 32],
                iconAnchor: [16, 16],
            });

            markerRef.current = L.marker([lat, lng], { icon }).addTo(mapInstanceRef.current);
        } else {
            mapInstanceRef.current.setView([lat, lng], 15);
            if (markerRef.current) {
                markerRef.current.setLatLng([lat, lng]);
            }
        }

        return () => {
            if (mapInstanceRef.current) {
                mapInstanceRef.current.remove();
                mapInstanceRef.current = null;
            }
        };
    }, [lat, lng]);

    return <div ref={mapContainerRef} className="w-full h-full z-0" />;
}
