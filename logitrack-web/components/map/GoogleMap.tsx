"use client";

/**
 * The one Google Maps entry of the web (GMAPS): every map renders through `GoogleMap`, which loads the
 * Maps JavaScript API with our browser key `NEXT_PUBLIC_GOOGLE_MAPS_API_KEY` (HTTP-referrer restricted,
 * Maps JavaScript API + Places API (New) only). Without a key (dev, CI, Vitest) it shows a "map
 * unavailable" state and loads nothing from Google. Import it only from a lazily loaded module
 * (eslint.config.mjs LAZY_MODULES).
 */
import { useEffect, type ReactNode } from "react";
import { APIProvider, AdvancedMarker, InfoWindow, Map, useAdvancedMarkerRef, useMap, type MapProps } from "@vis.gl/react-google-maps";

import { useLanguage } from "@/context/language";
import { cn } from "@/lib/utils";

export type LatLng = { lat: number; lng: number };

export const THAILAND_CENTER: LatLng = { lat: 13.7563, lng: 100.5018 };

/** Read at render so Next inlines the build value and tests can stub it. */
export function googleMapsApiKey(): string {
    return process.env.NEXT_PUBLIC_GOOGLE_MAPS_API_KEY ?? "";
}

/** Advanced markers need a Map ID; Google's `DEMO_MAP_ID` (default style) until we set our own. */
function googleMapsMapId(): string {
    return process.env.NEXT_PUBLIC_GOOGLE_MAPS_MAP_ID || "DEMO_MAP_ID";
}

/**
 * A Google map filling its parent. `isolate` keeps Google's internal z-index values inside the map, under
 * the layering scale (DESIGN.md). The 44 px controls are the phone touch target of the shared controls.
 */
export function GoogleMap({ className, children, ...props }: MapProps) {
    const { t } = useLanguage();
    const apiKey = googleMapsApiKey();
    if (!apiKey) {
        return (
            <div role="status" className={cn("flex h-full w-full items-center justify-center bg-muted/30 p-4 text-center text-sm text-muted-foreground", className)}>
                {t("common.map.unavailable")}
            </div>
        );
    }
    return (
        <APIProvider apiKey={apiKey} region="TH">
            <Map
                mapId={googleMapsMapId()}
                gestureHandling="greedy"
                disableDefaultUI
                zoomControl
                controlSize={44}
                clickableIcons={false}
                {...props}
                className={cn("isolate h-full w-full", className)}
            >
                {children}
            </Map>
        </APIProvider>
    );
}

/** Moves the camera to `position` (and `zoom`) whenever they change, like Leaflet's flyTo. */
export function PanTo({ position, zoom }: { position?: LatLng | null; zoom?: number }) {
    const map = useMap();
    const lat = position?.lat;
    const lng = position?.lng;
    useEffect(() => {
        if (!map || lat == null || lng == null) return;
        map.panTo({ lat, lng });
        if (zoom != null) map.setZoom(zoom);
    }, [map, lat, lng, zoom]);
    return null;
}

/** Shows every point: one point at `singleZoom`, more fitted with `padding` and zoomed in no further than `maxZoom`. */
export function fitPoints(map: google.maps.Map, points: readonly LatLng[], opts: { padding: number; maxZoom: number; singleZoom: number }) {
    if (points.length === 0) return;
    if (points.length === 1) {
        map.setCenter(points[0]);
        map.setZoom(opts.singleZoom);
        return;
    }
    const bounds = new google.maps.LatLngBounds();
    for (const p of points) bounds.extend(p);
    map.fitBounds(bounds, opts.padding);
    google.maps.event.addListenerOnce(map, "idle", () => {
        if ((map.getZoom() ?? 0) > opts.maxZoom) map.setZoom(opts.maxZoom);
    });
}

/**
 * An advanced marker (Google's red pin, or `children` as its content) with an info window anchored to it
 * while `popup` is set (Leaflet's bound popup).
 */
export function MarkerWithPopup({
    position,
    title,
    onClick,
    popup,
    onPopupClose,
    children,
}: {
    position: LatLng;
    title?: string;
    onClick?: () => void;
    popup?: ReactNode;
    onPopupClose?: () => void;
    children?: ReactNode;
}) {
    const [ref, marker] = useAdvancedMarkerRef();
    return (
        <>
            <AdvancedMarker ref={ref} position={position} title={title} onClick={onClick}>
                {children}
            </AdvancedMarker>
            {popup != null && marker ? (
                <InfoWindow anchor={marker} onCloseClick={onPopupClose}>
                    {popup}
                </InfoWindow>
            ) : null}
        </>
    );
}
