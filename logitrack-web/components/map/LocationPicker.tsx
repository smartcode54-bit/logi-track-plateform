"use client";

import { useState, useEffect } from "react";
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
    searchPlaceholder?: string;
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



function SearchControl({ onChange, searchPlaceholder }: { onChange: (pos: { lat: number; lng: number }) => void; searchPlaceholder: string }) {
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
            searchLabel: searchPlaceholder,
        });

        map.addControl(searchControl);

        const onLocation = (event: L.LeafletEvent) => {
            const result = event as L.LeafletEvent & { location?: { x: number; y: number } };
            if (result.location) {
                onChange({ lat: Number(result.location.y), lng: Number(result.location.x) });
            }
        };
        map.on('geosearch/showlocation', onLocation);

        return () => {
            map.off('geosearch/showlocation', onLocation);
            map.removeControl(searchControl);
        };
    }, [map, onChange, searchPlaceholder]);

    return null;
}

export default function LocationPicker({ value, onChange, searchPlaceholder = "Search for a place..." }: LocationPickerProps) {
    // Default to Thailand center if no value
    const defaultCenter = { lat: 13.7563, lng: 100.5018 };

    // Generate a unique ID for this instance
    const [mapId] = useState(() => `map-${Math.random().toString(36).substr(2, 9)}`);

    // Workaround for "Map container is already initialized" error in Strict Mode / Dev
    useEffect(() => {
        return () => {
            const container = L.DomUtil.get(mapId);
            if (container) {
                // @ts-expect-error — leaflet internal property not in type definitions
                container._leaflet_id = null;
            }
        };
    }, [mapId]);

    return (
        <div className="location-picker h-[300px] w-full min-w-0 max-w-full rounded-md overflow-hidden border">
            <MapContainer
                id={mapId}
                key={mapId}
                center={value || defaultCenter}
                zoom={10}
                className="h-full w-full"
            >
                <style jsx global>{`
                    .location-picker .leaflet-geosearch-bar {
                        width: auto;
                        max-width: none;
                        margin-left: 62px;
                        margin-right: 10px;
                    }
                    .location-picker .leaflet-control-geosearch form input {
                        min-width: 0;
                        padding-right: 32px;
                    }
                    .leaflet-control-geosearch form input {
                        color: var(--foreground) !important;
                        background: var(--background) !important;
                    }
                    .leaflet-control-geosearch .results {
                        color: var(--foreground) !important;
                        background: var(--background) !important;
                    }
                    .leaflet-control-geosearch .results > .active,
                    .leaflet-control-geosearch .results > :hover {
                        color: var(--accent-foreground) !important;
                        background-color: var(--accent) !important;
                    }
                    @media (max-width: 767px) {
                        .location-picker .leaflet-control-geosearch button.reset {
                            min-width: 44px;
                            height: 44px;
                            line-height: 44px;
                        }
                        .location-picker .leaflet-control-geosearch form input {
                            height: 44px;
                            padding-right: 44px;
                        }
                        .location-picker .leaflet-control-geosearch .results > * {
                            min-height: 44px;
                            white-space: normal;
                        }
                    }
                `}</style>
                <TileLayer
                    attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors'
                    url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png"
                />
                <SearchControl onChange={onChange} searchPlaceholder={searchPlaceholder} />
                <LocationMarker position={value || null} onChange={onChange} />
            </MapContainer>
        </div>
    );
}
