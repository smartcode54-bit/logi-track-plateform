// TW8 (issue #88, M1 slice; Appendix E §E.10 row 7; developer-spec.md §10.14 item 9): the task boards'
// hub filter offers every hub of ['hubs'], not the first 50 (`hubs.slice(0, 50)` in first-mile, line-haul
// and job-assign). Over-cap fixtures: 60 hubs (above the old cap, rendered in full) and 404 hubs (the
// load seed's count, Appendix D: the open list is virtualised and still reaches the last hub).
import React, { useState } from "react";
import { readFileSync } from "fs";
import path from "path";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterAll, beforeAll, beforeEach, describe, expect, it } from "vitest";

import { LanguageProvider } from "@/context/language";
import { FILTER_COMBOBOX_VIRTUALIZE_ABOVE } from "@/components/filter-combobox";
import { scrollViewport, stubVirtualLayout } from "@/test-utils/virtualLayout";
import { selectHubFilterOptions, type HubRow } from "../api/selectors";
import { HubFilterCombobox } from "./HubFilterCombobox";

const OLD_CAP = 50;
const ROW = 32;

function hub(i: number, over: Partial<HubRow> = {}): HubRow {
    const code = `HUB-${String(i).padStart(4, "0")}`;
    return {
        "Hub Code": code,
        "Hub Name": `Hub ${i}`,
        "Hub Name Th": `ฮับ ${i}`,
        station_type: "HUB",
        linkedCustomerId: "",
        customerLinkKind: "",
        linkedCustomerName: undefined,
        lat: undefined,
        lng: undefined,
        source: "custom",
        id: `h-${i}`,
        ...over,
    };
}

function Board({ hubs, onChange }: { hubs: HubRow[]; onChange?: (v: string) => void }) {
    const [value, setValue] = useState("all");
    return (
        <>
            <HubFilterCombobox
                hubs={hubs}
                value={value}
                onChange={(v) => {
                    setValue(v);
                    onChange?.(v);
                }}
                allLabel="All hubs"
            />
            <output data-testid="selected">{value}</output>
        </>
    );
}

async function open(hubs: HubRow[]) {
    render(
        <LanguageProvider>
            <Board hubs={hubs} />
        </LanguageProvider>
    );
    const u = userEvent.setup();
    await u.click(await screen.findByRole("combobox"));
    return u;
}

let restore: () => void;
beforeAll(() => {
    restore = stubVirtualLayout({ viewport: 240, row: ROW });
});
afterAll(() => restore());
beforeEach(() => {
    try {
        window.localStorage.removeItem("language");
    } catch {
        // English is the default anyway
    }
});

describe("selectHubFilterOptions", () => {
    it("keeps every hub with a code, once per code, Thai name first", () => {
        const rows = [
            hub(1),
            hub(2, { "Hub Name Th": undefined }),
            hub(3, { "Hub Name Th": undefined, "Hub Name": undefined }),
            hub(4, { "Hub Code": "HUB-0001", "Hub Name Th": "ซ้ำ" }), // duplicate code: the first row wins
            hub(5, { "Hub Code": undefined }), // no code: could never match a task
            hub(6, { "Hub Code": "  " }),
        ];
        expect(selectHubFilterOptions(rows)).toEqual([
            { value: "HUB-0001", label: "ฮับ 1", searchText: "HUB-0001 Hub 1 ฮับ 1" },
            { value: "HUB-0002", label: "Hub 2", searchText: "HUB-0002 Hub 2" },
            { value: "HUB-0003", label: "HUB-0003", searchText: "HUB-0003" },
        ]);
    });

    it("has no cap", () => {
        expect(selectHubFilterOptions(Array.from({ length: 404 }, (_, i) => hub(i)))).toHaveLength(404);
    });
});

describe("HubFilterCombobox (over the old 50-hub cap)", () => {
    it("lists all 60 hubs of a list between the old cap and the virtualisation threshold", async () => {
        const hubs = Array.from({ length: OLD_CAP + 10 }, (_, i) => hub(i));
        expect(hubs.length).toBeLessThanOrEqual(FILTER_COMBOBOX_VIRTUALIZE_ABOVE);
        await open(hubs);
        // "All hubs" plus every hub: the 51st to the 60th were unreachable before.
        expect(screen.getAllByText("All hubs")).toHaveLength(2); // the trigger and the first row
        for (let i = 0; i < hubs.length; i++) expect(screen.getByText(`ฮับ ${i}`)).toBeInTheDocument();
    });

    it("404 hubs: the open list is virtualised, scrolls to the last hub, and finds any hub by code or English name", async () => {
        const hubs = Array.from({ length: 404 }, (_, i) => hub(i));
        const u = await open(hubs);
        // The virtualised list is its own chunk, preloaded when the combobox mounts.
        const list = await waitFor(() => {
            const el = document.querySelector<HTMLElement>('[data-slot="filter-combobox-virtual-list"]');
            expect(el).not.toBeNull();
            return el;
        });
        const rendered = () => list!.querySelectorAll("[data-index]").length;
        expect(rendered()).toBeGreaterThan(0);
        expect(rendered()).toBeLessThan(40);
        expect(screen.queryByText("ฮับ 403")).toBeNull();

        scrollViewport(list!, "end", ROW, hubs.length + 1);
        expect(within(list!).getByText("ฮับ 403")).toBeInTheDocument();
        expect(rendered()).toBeLessThan(40);

        // Search by code (beyond the old cap), then choose it: the board filters on the code.
        await u.type(screen.getByPlaceholderText("Search by name or code..."), "hub-0399");
        await waitFor(() => expect(screen.getByText("ฮับ 399")).toBeInTheDocument());
        await u.click(screen.getByText("ฮับ 399"));
        expect(screen.getByTestId("selected")).toHaveTextContent("HUB-0399");
        expect(screen.getByRole("combobox")).toHaveTextContent("ฮับ 399");

        // English name search reaches the end of the list too.
        await u.click(screen.getByRole("combobox"));
        await u.type(screen.getByPlaceholderText("Search by name or code..."), "Hub 402");
        await waitFor(() => expect(screen.getByText("ฮับ 402")).toBeInTheDocument());

        // A search with no hub says so.
        await u.clear(screen.getByPlaceholderText("Search by name or code..."));
        await u.type(screen.getByPlaceholderText("Search by name or code..."), "nowhere");
        expect(await screen.findByText("No location found.")).toBeInTheDocument();
    });

    it("clears back to every hub", async () => {
        const u = await open(Array.from({ length: 120 }, (_, i) => hub(i)));
        await u.type(screen.getByPlaceholderText("Search by name or code..."), "HUB-0110");
        await u.click(await screen.findByText("ฮับ 110"));
        expect(screen.getByTestId("selected")).toHaveTextContent("HUB-0110");
        await u.click(screen.getByRole("button", { name: "Show every location" }));
        expect(screen.getByTestId("selected")).toHaveTextContent("all");
    });
});

describe("task boards", () => {
    // The three Firestore boards (moved to features/tasks/legacy by T34) render the shared combobox over the full
    // ['hubs'] rows; no slice survives.
    it.each([
        "features/tasks/legacy/LegacyFirstMilePage.tsx",
        "features/tasks/legacy/LegacyLineHaulPage.tsx",
        "features/tasks/legacy/LegacyJobAssignPage.tsx",
    ])("%s", (file) => {
        const src = readFileSync(path.join(process.cwd(), file), "utf8");
        expect(src).not.toMatch(/hubs\.slice\(/);
        expect(src).toMatch(/<HubFilterCombobox\s+hubs=\{hubRows \?\? NO_HUB_ROWS\}/);
    });

    // The Go board (T34) offers every source of the loaded day and every hub of the Go master in its pickers.
    it("features/tasks/components/go: no slice of the hub lists", () => {
        for (const file of ["TaskBoardPage.tsx", "opsShared.tsx", "TaskFormDialog.tsx"]) {
            const src = readFileSync(path.join(process.cwd(), "features/tasks/components/go", file), "utf8");
            expect(src, file).not.toMatch(/(hubs|hubOptions|list|ordered|shown)\.slice\(/);
        }
    });
});
