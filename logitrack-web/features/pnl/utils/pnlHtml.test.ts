import { beforeEach, describe, expect, it } from "vitest";
import { computePnl, recomputePnlScenario, type PnlInputs } from "@/lib/pnlCompute";
import type { PnlRateRow } from "@/validate/pnlSettingsSchema";
import { buildPnlHtml, buildPnlHtmlData, jsonForScript } from "./pnlHtml";
import { pnlHtmlClient } from "./pnlHtmlClient";

const bkk = (y: number, m: number, d: number, h = 12) => new Date(Date.UTC(y, m - 1, d, h - 7));
const rate = (kind: PnlRateRow["kind"], amountThb: number): PnlRateRow => ({ id: kind, kind, amountThb, effectiveFrom: "2026-10-01", voided: false });
const rates = [rate("driver_trip", 250), rate("helper_day", 400), rate("maint_service_km", 0.14), rate("maint_tyre_km", 0.3)];
const t = (key: string, params?: Record<string, string | number>) => (params ? `${key} ${JSON.stringify(params)}` : key);

const EVIL = "</script><img src=x onerror=alert(1)>";

function inputs(): PnlInputs {
    return {
        year: 2026, month: 10, now: bkk(2026, 11, 16),
        trips: [0, 1].map((i) => ({
            id: `t${i}`, status: "delivered", truckId: "T1", revenueThb: 2000, checkInAt: bkk(2026, 10, 2 + i, 8),
            deliveredAt: bkk(2026, 10, 2 + i, 14), originCode: "H2", destinationCodes: ["SOCE"], isMultiDrop: false,
            hasTask: true, helperDriverIds: [], driverName: EVIL,
        })),
        standbys: [], helperTasks: [],
        trucks: [{ id: "T1", plate: "70-1111", ownershipType: "own", truckStatus: "active", insurancePremium: 24000, taxExpense: 3600 }],
        distances: [
            { table: "hub_soc", hubId: "H2", socId: "SOCE", distanceKm: 125 },
            { table: "soc_hub", hubId: "H2", socId: "SOCE", distanceKm: 125 },
        ],
        fuelRows: [
            { id: "f0", truckId: "T1", date: bkk(2026, 9, 29), amount: 2500, odometer: 100000, status: "APPROVED" },
            { id: "f1", truckId: "T1", date: bkk(2026, 10, 30), amount: 10000, odometer: 102500, status: "APPROVED" },
        ],
        otherExpenses: [], maintenance: [], renewals: [], rates, fixedCosts: [],
        driverIdentity: {}, planBasisCustomerIds: [],
    };
}

const params = () => ({ result: computePnl(inputs()), hubLabels: {}, rates, fixedCosts: [], t, language: "th", now: bkk(2026, 11, 16) });

describe("P&L HTML export (R21)", () => {
    beforeEach(() => {
        document.body.innerHTML = '<div id="app"></div>';
    });

    it("cannot be broken out of by data inside the embedded JSON", () => {
        expect(jsonForScript({ name: EVIL })).not.toContain("</script>");
        const html = buildPnlHtml(params());
        expect(html.split("</script>").length - 1).toBe(2); // the data block + the code block, nothing else
        expect(html).not.toContain("<img src=x");
    });

    it("renders the month and recomputes when a rate is edited", () => {
        const data = buildPnlHtmlData(params());
        // Re-create both functions from source, exactly as the file does.
        const recompute = new Function(`return (${recomputePnlScenario.toString()})`)();
        const client = new Function(`return (${pnlHtmlClient.toString()})`)();
        client(JSON.parse(jsonForScript(data)), recompute);

        const app = document.getElementById("app") as HTMLElement;
        const cellTexts = () => Array.from(app.querySelectorAll("td")).map((td) => td.textContent);
        // 250 km × ฿4/km = 1,000 fuel; 250 × 0.44 = 110 maintenance; 2,000 − 1,000 − 250 − 110 = 640
        expect(cellTexts()).toContain("640.00");
        expect(app.textContent).toContain(EVIL); // shown as text…
        expect(app.querySelector("img")).toBeNull(); // …never as markup

        const driverInput = app.querySelector("input") as HTMLInputElement;
        expect(driverInput.value).toBe("250");
        driverInput.value = "300";
        driverInput.dispatchEvent(new Event("input"));
        expect(cellTexts()).toContain("590.00");
        expect(cellTexts()).not.toContain("640.00");

        (app.querySelector("button") as HTMLButtonElement).click();
        expect(driverInput.value).toBe("250");
        expect(cellTexts()).toContain("640.00");
    });
});
