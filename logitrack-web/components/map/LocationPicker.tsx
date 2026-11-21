"use client";

import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { AdvancedMarker, useMapsLibrary } from "@vis.gl/react-google-maps";

import { Input } from "@/components/ui/input";
import { useLanguage } from "@/context/language";
import { GoogleMap, PanTo, THAILAND_CENTER, type LatLng } from "./GoogleMap";

interface LocationPickerProps {
    value?: LatLng;
    searchPlaceholder?: string;
    onChange: (value: LatLng) => void;
}

/** Thailand's bounding box: results prefer it but are not restricted to it (Places API (New) `locationBias`). */
const THAILAND_BOUNDS = { south: 5.6, west: 97.3, north: 20.5, east: 105.7 };

/** Click the map, or search a place or address, to set the pin. */
export default function LocationPicker({ value, onChange, searchPlaceholder }: LocationPickerProps) {
    return (
        <div className="location-picker relative h-[300px] w-full min-w-0 max-w-full rounded-md overflow-hidden border">
            <GoogleMap
                defaultCenter={value ?? THAILAND_CENTER}
                defaultZoom={10}
                onClick={(e) => {
                    if (e.detail.latLng) onChange(e.detail.latLng);
                }}
            >
                <PlaceSearch placeholder={searchPlaceholder} onPick={onChange} />
                <PanTo position={value} />
                {value ? <AdvancedMarker position={value} /> : null}
            </GoogleMap>
        </div>
    );
}

type Results = { input: string; items: google.maps.places.PlacePrediction[] };

/** Places API (New) Autocomplete over the map: typing lists places, picking one moves the pin. */
function PlaceSearch({ placeholder, onPick }: { placeholder?: string; onPick: (pos: LatLng) => void }) {
    const { t, language } = useLanguage();
    const places = useMapsLibrary("places");
    const [query, setQuery] = useState("");
    const [picked, setPicked] = useState<string | null>(null);
    const [results, setResults] = useState<Results | null>(null);
    const [failed, setFailed] = useState(false);
    // One billing session runs from the first keystroke to the pick's fetchFields.
    const session = useRef<google.maps.places.AutocompleteSessionToken | null>(null);

    const input = query.trim();
    const searching = input.length >= 2 && query !== picked;

    useEffect(() => {
        if (!places || !searching) return;
        let live = true;
        const timer = setTimeout(() => {
            session.current ??= new places.AutocompleteSessionToken();
            places.AutocompleteSuggestion.fetchAutocompleteSuggestions({
                input,
                sessionToken: session.current,
                locationBias: THAILAND_BOUNDS,
                region: "th",
                language,
            })
                .then(({ suggestions }) => {
                    if (live) setResults({ input, items: suggestions.flatMap((s) => (s.placePrediction ? [s.placePrediction] : [])) });
                })
                .catch(() => {
                    if (live) setFailed(true);
                });
        }, 250);
        return () => {
            live = false;
            clearTimeout(timer);
        };
    }, [places, searching, input, language]);

    const items = searching && !failed && results?.input === input ? results.items : null;

    const pick = async (prediction: google.maps.places.PlacePrediction) => {
        const text = prediction.text.toString();
        setQuery(text);
        setPicked(text);
        try {
            const place = prediction.toPlace();
            await place.fetchFields({ fields: ["location"] });
            session.current = null;
            if (place.location) onPick({ lat: place.location.lat(), lng: place.location.lng() });
            else setFailed(true);
        } catch {
            setFailed(true);
        }
    };

    const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
        // The picker sits inside forms: Enter picks the first place instead of submitting.
        if (e.key === "Enter") {
            e.preventDefault();
            if (items?.[0]) void pick(items[0]);
        } else if (e.key === "Escape" && items) {
            e.preventDefault();
            setPicked(query);
        }
    };

    const label = placeholder ?? t("common.map.searchPlaceholder");
    return (
        <div className="absolute inset-x-2 top-2 z-raised">
            <Input
                type="search"
                value={query}
                onChange={(e) => {
                    setQuery(e.target.value);
                    setFailed(false);
                }}
                onKeyDown={onKeyDown}
                placeholder={label}
                aria-label={label}
                autoComplete="off"
                className="bg-background dark:bg-background shadow-sm"
            />
            {items ? (
                <ul aria-label={label} className="mt-1 max-h-48 overflow-y-auto rounded-md border bg-popover text-popover-foreground shadow-md">
                    {items.length === 0 ? (
                        <li className="px-3 py-2 text-sm text-muted-foreground">{t("common.noResults")}</li>
                    ) : (
                        items.map((p) => (
                            <li key={p.placeId}>
                                <button
                                    type="button"
                                    onClick={() => void pick(p)}
                                    className="flex min-h-11 w-full flex-col justify-center px-3 py-1.5 text-left text-sm hover:bg-accent hover:text-accent-foreground focus-visible:bg-accent focus-visible:text-accent-foreground focus-visible:outline-none md:min-h-0"
                                >
                                    <span>{(p.mainText ?? p.text).toString()}</span>
                                    {p.secondaryText ? <span className="text-xs text-muted-foreground">{p.secondaryText.toString()}</span> : null}
                                </button>
                            </li>
                        ))
                    )}
                </ul>
            ) : null}
            {failed ? (
                <p role="alert" className="mt-1 rounded-md border bg-background px-3 py-1.5 text-xs text-destructive shadow-sm">
                    {t("common.map.searchFailed")}
                </p>
            ) : null}
        </div>
    );
}
