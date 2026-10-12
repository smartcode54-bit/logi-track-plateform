// T54R review (issue #74): the freshness of ['vehicleLocations'] with the tab's stream open. The batches
// of vehicle_locations.updated move the known trucks only, and a dispatcher's stream carries no batch of
// the carrier trucks it reads (p_dispatch_read), so the key keeps a poll counted from the server's last
// answer: 60 s for a dispatcher or a closed stream, 5 min for staff, whatever the batches wrote between.
import React from "react";
import { act, renderHook } from "@testing-library/react";
import { focusManager, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { queryKeys } from "@/lib/queryKeys";
import { RealtimeInvalidator } from "@/lib/realtime/invalidator";
import { POLL_INTERVAL_MS } from "@/lib/realtime/polling";
import { setRealtimeStatus } from "@/lib/realtime/status";
import type { VehicleLocationDTO } from "./vehicleLocationEvents";
import { useVehicleLocations, VEHICLE_LOCATIONS_STREAM_REFRESH_MS, vehicleLocationsRefresh } from "./vehicleLocations";

const PATH = "/api/go/v1/vehicle-locations";
const SYNC_MS = 3 * 60_000; // one cartrack.sync slot

/** Moves the fake clock by `ms` inside `act`, so the fetches it starts land in the hook's render. */
async function tick(ms = 0) {
    await act(async () => {
        await vi.advanceTimersByTimeAsync(ms);
    });
}

function row(truckId: string, over: Partial<VehicleLocationDTO> = {}): VehicleLocationDTO {
    return {
        truckId,
        tenantId: "t-own",
        licensePlate: `70-${truckId}`,
        lat: 13.7,
        lng: 100.5,
        speedKmh: 40,
        heading: 90,
        lastGpsAt: "2026-10-12T01:00:00Z",
        updatedAt: "2026-10-12T01:00:05Z",
        engineOn: true,
        positionDescription: "Bang Na",
        ...over,
    };
}

/** The hook on a fresh client, `['me']` already known, the server's answer settable. */
function mount(dispatcher: boolean) {
    const web = fakeWeb();
    const server = { rows: [row("a")] };
    const me = makeMe({ dispatcher, capabilities: ["fleet:view_live_map", ...(dispatcher ? ["dispatch:view_operations"] : [])] });
    web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
    web.on("GET", PATH, () => json(200, { data: server.rows }));
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.setQueryData(queryKeys.me(), me);
    const inv = new RealtimeInvalidator({ client, isVisible: () => true });
    const wrapper = ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const view = renderHook(() => useVehicleLocations(true), { wrapper });
    let minute = 3;
    return {
        server,
        /** What the page renders from: the cached list. */
        data: () => client.getQueryData<VehicleLocationDTO[]>(queryKeys.vehicleLocations()),
        fetches: () => web.count("GET", PATH),
        /** One cartrack.sync batch: every listed truck moved (a fresh fix each time). */
        batch(truckIds: string[] = ["a"]) {
            const at = new Date(Date.parse("2026-10-12T01:00:00Z") + minute++ * 60_000).toISOString();
            act(() => inv.apply({ event: "vehicle_locations.updated", data: { positions: truckIds.map((truckId) => ({ truckId, lat: 13.8, lng: 100.7, speed: 50, heading: 180, at })) } }));
        },
        stop() {
            inv.dispose();
            view.unmount();
            client.clear();
        },
    };
}

beforeEach(() => {
    vi.useFakeTimers();
    focusManager.setFocused(true);
});

afterEach(() => {
    focusManager.setFocused(undefined);
    setRealtimeStatus("idle");
    vi.unstubAllGlobals();
    vi.useRealTimers();
});

describe("['vehicleLocations'] freshness", () => {
    it("polls 60 s for a dispatcher with the stream open (its stream carries no batch of the carrier trucks it reads)", async () => {
        expect(vehicleLocationsRefresh(true, true).refetchInterval({ state: { dataUpdatedAt: Date.now(), errorUpdatedAt: 0 } })).toBe(POLL_INTERVAL_MS);
        setRealtimeStatus("open");
        const h = mount(true);
        await tick(0);
        expect(h.fetches()).toBe(1);
        await tick(POLL_INTERVAL_MS);
        expect(h.fetches()).toBe(2);
        h.batch(); // its own tenant's batch moves the trucks it lists and does not postpone the poll
        await tick(POLL_INTERVAL_MS);
        expect(h.fetches()).toBe(3);
        h.stop();
    });

    it("does not poll every 60 s for staff with the stream open, but refetches 5 min after the last answer despite the 3-min batches", async () => {
        expect(vehicleLocationsRefresh(true, false).refetchInterval({ state: { dataUpdatedAt: Date.now(), errorUpdatedAt: 0 } })).toBe(VEHICLE_LOCATIONS_STREAM_REFRESH_MS);
        setRealtimeStatus("open");
        const h = mount(false);
        await tick(0);
        expect(h.fetches()).toBe(1);
        await tick(POLL_INTERVAL_MS);
        expect(h.fetches()).toBe(1);

        // The truck drives on and its engine stops; the batch carries the position only.
        h.server.rows = [row("a", { positionDescription: "Lat Krabang", engineOn: false, lat: 13.8, lng: 100.7 })];
        await tick(SYNC_MS - POLL_INTERVAL_MS);
        h.batch();
        await tick();
        expect(h.data()?.[0]).toMatchObject({ lat: 13.8, lng: 100.7, positionDescription: "Bang Na", engineOn: true });
        expect(h.fetches()).toBe(1);

        await tick(VEHICLE_LOCATIONS_STREAM_REFRESH_MS - SYNC_MS - 1);
        expect(h.fetches()).toBe(1);
        await tick(1);
        expect(h.fetches()).toBe(2);
        await tick();
        expect(h.data()?.[0]).toMatchObject({ positionDescription: "Lat Krabang", engineOn: false });

        // A steady stream of batches (one every 3 min) still lets it refetch every 5 min, which drops a pruned truck.
        h.server.rows = [];
        h.batch();
        await tick(SYNC_MS);
        h.batch();
        await tick(VEHICLE_LOCATIONS_STREAM_REFRESH_MS - SYNC_MS);
        expect(h.fetches()).toBe(3);
        await tick();
        expect(h.data()).toEqual([]);
        h.stop();
    });

    it("polls 60 s for staff while the stream is not open", async () => {
        expect(vehicleLocationsRefresh(false, false).refetchInterval({ state: { dataUpdatedAt: Date.now(), errorUpdatedAt: 0 } })).toBe(POLL_INTERVAL_MS);
        setRealtimeStatus("down");
        const h = mount(false);
        await tick(0);
        await tick(POLL_INTERVAL_MS);
        expect(h.fetches()).toBe(2);
        h.stop();
    });

    it("refetches at once when a batch names a truck the list lacks (its first fix after the page loaded)", async () => {
        setRealtimeStatus("open");
        const h = mount(false);
        await tick(0);
        expect(h.fetches()).toBe(1);
        h.server.rows = [row("a"), row("b", { licensePlate: "70-NEW" })];
        h.batch(["a", "b"]);
        await tick(1_000); // the 500 ms coalescing window
        expect(h.fetches()).toBe(2);
        expect(h.data()?.map((r) => r.truckId)).toEqual(["a", "b"]);
        h.stop();
    });
});
