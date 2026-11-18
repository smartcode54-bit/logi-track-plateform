// GMAPS: the location picker on Google Maps. Place search (Places API (New) Autocomplete, biased to Thailand)
// moves the pin and sets lat/lng; without NEXT_PUBLIC_GOOGLE_MAPS_API_KEY the map shows "map unavailable" and
// loads nothing from Google. The Google loader (@vis.gl/react-google-maps) is mocked.
import React, { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

const google = vi.hoisted(() => ({ fetchSuggestions: vi.fn(), apiProvider: vi.fn() }));

vi.mock("@vis.gl/react-google-maps", () => ({
    APIProvider: ({ apiKey, children }: { apiKey: string; children: React.ReactNode }) => {
        google.apiProvider(apiKey);
        return <>{children}</>;
    },
    Map: ({ children, onClick }: { children: React.ReactNode; onClick?: (e: unknown) => void }) => (
        <div data-testid="map">
            {children}
            <button type="button" data-testid="map-surface" onClick={() => onClick?.({ detail: { latLng: { lat: 14.1, lng: 101.2 } } })} />
        </div>
    ),
    AdvancedMarker: ({ position }: { position: { lat: number; lng: number } }) => <div data-testid="pin" data-lat={position.lat} data-lng={position.lng} />,
    useMap: () => null,
    useMapsLibrary: (name: string) =>
        name === "places"
            ? { AutocompleteSessionToken: class {}, AutocompleteSuggestion: { fetchAutocompleteSuggestions: google.fetchSuggestions } }
            : null,
}));

import { LanguageProvider } from "@/context/language";

// A lazy module (eslint LAZY_MODULES): loaded the way the pages load it.
let LocationPicker: typeof import("../LocationPicker").default;
beforeAll(async () => {
    LocationPicker = (await import("../LocationPicker")).default;
});

type LatLng = { lat: number; lng: number };

function prediction(placeId: string, main: string, secondary: string, at: LatLng) {
    return {
        placePrediction: {
            placeId,
            text: { toString: () => `${main}, ${secondary}` },
            mainText: { toString: () => main },
            secondaryText: { toString: () => secondary },
            toPlace() {
                const place: { location: { lat: () => number; lng: () => number } | null; fetchFields: () => Promise<void> } = {
                    location: null,
                    fetchFields: async () => {
                        place.location = { lat: () => at.lat, lng: () => at.lng };
                    },
                };
                return place;
            },
        },
    };
}

const CENTRAL_WORLD = { lat: 13.7466, lng: 100.5393 };

function Harness({ onChange }: { onChange: (pos: LatLng) => void }) {
    const [value, setValue] = useState<LatLng>();
    return (
        <LocationPicker
            value={value}
            onChange={(pos) => {
                onChange(pos);
                setValue(pos);
            }}
        />
    );
}

function renderPicker(onChange = vi.fn(), wrap: (ui: React.ReactElement) => React.ReactElement = (ui) => ui) {
    render(<LanguageProvider>{wrap(<Harness onChange={onChange} />)}</LanguageProvider>);
    return onChange;
}

let language = "en";
beforeEach(() => {
    language = "en";
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => language);
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {});
    google.fetchSuggestions.mockReset();
    google.apiProvider.mockReset();
});
afterEach(() => {
    vi.unstubAllEnvs();
    vi.restoreAllMocks();
});

describe("LocationPicker with a Google Maps key", () => {
    beforeEach(() => vi.stubEnv("NEXT_PUBLIC_GOOGLE_MAPS_API_KEY", "test-browser-key"));

    it("searches places biased to Thailand; picking one moves the pin and sets lat/lng", async () => {
        google.fetchSuggestions.mockResolvedValue({
            suggestions: [prediction("p1", "CentralWorld", "Pathum Wan, Bangkok", CENTRAL_WORLD), { placePrediction: null }],
        });
        const user = userEvent.setup();
        const onChange = renderPicker();

        const box = await screen.findByRole("searchbox", { name: "Search for a place or address" });
        expect(google.apiProvider).toHaveBeenCalledWith("test-browser-key");
        expect(screen.queryByTestId("pin")).toBeNull();
        await user.type(box, "Central World");
        const option = await screen.findByRole("button", { name: /CentralWorld/ });
        expect(option).toHaveTextContent("Pathum Wan, Bangkok");
        expect(google.fetchSuggestions).toHaveBeenLastCalledWith(
            expect.objectContaining({
                input: "Central World",
                region: "th",
                language: "en",
                locationBias: { south: 5.6, west: 97.3, north: 20.5, east: 105.7 },
                sessionToken: expect.anything(),
            })
        );

        await user.click(option);
        await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith(CENTRAL_WORLD));
        const pin = screen.getByTestId("pin");
        expect(pin).toHaveAttribute("data-lat", String(CENTRAL_WORLD.lat));
        expect(pin).toHaveAttribute("data-lng", String(CENTRAL_WORLD.lng));
        expect(screen.getByRole("searchbox")).toHaveValue("CentralWorld, Pathum Wan, Bangkok");
        expect(screen.queryByRole("button", { name: /CentralWorld/ })).toBeNull();
    });

    it("Enter picks the first place and never submits the surrounding form", async () => {
        google.fetchSuggestions.mockResolvedValue({ suggestions: [prediction("p1", "CentralWorld", "Bangkok", CENTRAL_WORLD)] });
        const submit = vi.fn((e: React.FormEvent) => e.preventDefault());
        const user = userEvent.setup();
        const onChange = renderPicker(vi.fn(), (ui) => <form onSubmit={submit}>{ui}</form>);

        const box = await screen.findByRole("searchbox");
        await user.type(box, "Central");
        await screen.findByRole("button", { name: /CentralWorld/ });
        await user.type(box, "{Enter}");
        await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith(CENTRAL_WORLD));
        expect(submit).not.toHaveBeenCalled();
    });

    it("a click on the map still sets lat/lng", async () => {
        const user = userEvent.setup();
        const onChange = renderPicker();
        await user.click(await screen.findByTestId("map-surface"));
        expect(onChange).toHaveBeenCalledWith({ lat: 14.1, lng: 101.2 });
        expect(screen.getByTestId("pin")).toHaveAttribute("data-lat", "14.1");
    });

    it("says so when the place search fails, and shows no results for an empty answer", async () => {
        google.fetchSuggestions.mockRejectedValueOnce(new Error("REQUEST_DENIED")).mockResolvedValue({ suggestions: [] });
        const user = userEvent.setup();
        renderPicker();
        const box = await screen.findByRole("searchbox");
        await user.type(box, "zz");
        expect(await screen.findByRole("alert")).toHaveTextContent("Place search failed. Try again or click the map.");
        await user.type(box, "z");
        expect(await screen.findByText("No results")).toBeInTheDocument();
        expect(screen.queryByRole("alert")).toBeNull();
    });
});

describe("LocationPicker without a Google Maps key", () => {
    beforeEach(() => vi.stubEnv("NEXT_PUBLIC_GOOGLE_MAPS_API_KEY", ""));

    it("shows the map-unavailable state and loads nothing from Google (en)", async () => {
        renderPicker();
        expect(await screen.findByText("Map unavailable: no Google Maps key is set for this build.")).toHaveAttribute("role", "status");
        expect(google.apiProvider).not.toHaveBeenCalled();
        expect(screen.queryByTestId("map")).toBeNull();
        expect(screen.queryByRole("searchbox")).toBeNull();
    });

    it("in Thai", async () => {
        language = "th";
        renderPicker();
        expect(await screen.findByText("แผนที่ใช้งานไม่ได้: บิลด์นี้ยังไม่ได้ตั้งค่าคีย์ Google Maps")).toHaveAttribute("role", "status");
    });
});
