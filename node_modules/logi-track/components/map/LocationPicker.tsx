"use client";

import { useState, useMemo, useEffect } from "react";
import { MapContainer, TileLayer, Marker, useMapEvents, useMap } from "react-leaflet";
import "leaflet/dist/leaflet.css";
import L from "leaflet";
import { GeoSearchControl, OpenStreetMapProvider } from 'leaflet-geosearch';
import 'leaflet-geosearch/dist/geosearch.css';

// Fix Leaflet marker icon issue in Next.js
const icon = L.icon({
    iconUrl: "https://unpkg.com/leaflet@1.9.4/dist/images/marker-icon.png",
    shadowUrl: "https://unpkg.com/leaflet@1.9.4/dist/images/marker-shadow.png",
    iconSize: [25, 41],
    iconAnchor: [12, 41],
});

interface LocationPickerProps {
    value?: { lat: number; lng: number };
    onChange: (value: { lat: number; lng: number }) => void;
}

function LocationMarker({ position, onChange }: { position: { lat: number; lng: number } | null, onChange: (pos: { lat: number; lng: number }) => void }) {
    const map = useMap();

    useEffect(() => {
        if (position) {
            map.flyTo(position, map.getZoom());
        }
    }, [position, map]);

    useMapEvents({
        click(e: L.LeafletMouseEvent) {
            onChange(e.latlng);
        },
    });

    return position ? <Marker position={position} icon={icon} /> : null;
}



function SearchControl({ onChange }: { onChange: (pos: { lat: number; lng: number }) => void }) {
    const map = useMap();

    useEffect(() => {
        const provider = new OpenStreetMapProvider();

        const searchControl = new (GeoSearchControl as any)({
            provider,
            style: 'bar',
            showMarker: false,
            showPopup: false,
            autoClose: true,
            retainZoomLevel: false,
            animateZoom: true,
            keepResult: false,
            searchLabel: 'Search for a place...',
        });

        map.addControl(searchControl);

        map.on('geosearch/showlocation', (result: any) => {
            if (result.location) {
                onChange({ lat: Number(result.location.y), lng: Number(result.location.x) });
            }
        });

        return () => {
            map.removeControl(searchControl);
        };
    }, [map, onChange]);

    return null;
}

export default function LocationPicker({ value, onChange }: LocationPickerProps) {
    // Default to Thailand center if no value
    const defaultCenter = { lat: 13.7563, lng: 100.5018 };

    // Generate a unique ID for this instance
    const [mapId] = useState(() => `map-${Math.random().toString(36).substr(2, 9)}`);

    // Workaround for "Map container is already initialized" error in Strict Mode / Dev
    useEffect(() => {
        return () => {
            const container = L.DomUtil.get(mapId);
            if (container) {
                // @ts-ignore
                container._leaflet_id = null;
            }
        };
    }, [mapId]);

    return (
        <div className="h-[300px] w-full rounded-md overflow-hidden border">
            <MapContainer
                id={mapId}
                key={mapId}
                center={value || defaultCenter}
                zoom={10}
                style={{ height: "100%", width: "100%" }}
            >
                <style jsx global>{`
                    .leaflet-control-geosearch form input {
                        color: black !important;
                    }
                    .leaflet-control-geosearch .results {
                        color: black !important;
                    }
                `}</style>
                <TileLayer
                    attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
                    url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png"
                />
                <SearchControl onChange={onChange} />
                <LocationMarker position={value || null} onChange={onChange} />
            </MapContainer>
        </div>
    );
}
