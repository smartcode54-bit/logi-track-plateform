/**
 * Browser renderer of the exported P&L file (spec R21). Embedded into the HTML through
 * `pnlHtmlClient.toString()` and called as `pnlHtmlClient(DATA, recomputePnlScenario)`, so it is
 * SELF-CONTAINED: no imports at runtime (types only), no module helpers, no spread / for…of.
 *
 * Every value from the data is written with `textContent` — never innerHTML — so names and routes
 * from Firestore cannot inject markup into the file.
 */
import type { PnlScenario, PnlScenarioOverrides, PnlScenarioResult } from "@/lib/pnlCompute";

export interface PnlHtmlCell {
    t: string;
    num?: boolean;
    cls?: string;
}

export interface PnlHtmlData {
    scenario: PnlScenario;
    labels: Record<string, string>;
    rateKeys: Array<{ key: "driverTrip" | "helperDay" | "maintServiceKm" | "maintTyreKm"; label: string; unit: string }>;
    banners: string[];
    rows: Array<{ cells: string[]; badges: Array<{ t: string; bad: boolean }>; counted: boolean }>;
    lanes: Record<string, { customer: string; route: string; vehicleClass: string }>;
    trucks: Array<{
        plate: string; trips: string; revenue: number; jobKm: number; odoKm: number | null; offPlanKm: number | null;
        offPlanPct: number | null; fuelRate: number | null; fleetAvg: boolean; tripFuel: number; offPlanFuel: number | null; maintBills: number;
    }>;
    statementLabels: Record<string, string>;
    fixedItems: Array<{ label: string; amount: number }>;
    subcontractor: string;
    warnings: Array<{ t: string; refs: string; bad: boolean }>;
    settingsUsed: string[];
}

export function pnlHtmlClient(D: PnlHtmlData, recompute: (s: PnlScenario, o: PnlScenarioOverrides) => PnlScenarioResult): void {
    const L = D.labels;
    function fill(tpl: string, vars: Record<string, string | number>): string {
        let out = tpl;
        const keys = Object.keys(vars);
        for (let i = 0; i < keys.length; i++) out = out.split("{" + keys[i] + "}").join(String(vars[keys[i]]));
        return out;
    }
    function money(n: number | null | undefined): string {
        if (n === null || n === undefined || !isFinite(n)) return "—";
        const v = Math.abs(n) < 0.005 ? 0 : n; // no "-0.00" on empty cost lines
        return v.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
    }
    function num(n: number | null | undefined, digits: number): string {
        if (n === null || n === undefined || !isFinite(n)) return "—";
        return n.toLocaleString("en-US", { maximumFractionDigits: digits });
    }
    function pct(n: number | null | undefined): string {
        return n === null || n === undefined || !isFinite(n) ? "—" : n.toFixed(1) + "%";
    }
    function el(tag: string, cls?: string, text?: string): HTMLElement {
        const e = document.createElement(tag);
        if (cls) e.className = cls;
        if (text !== undefined) e.textContent = text;
        return e;
    }
    function section(title: string): HTMLElement {
        const s = el("section", "card");
        s.appendChild(el("h2", "", title));
        return s;
    }
    function table(head: string[], body: PnlHtmlCell[][], rowCls?: string[]): HTMLElement {
        const wrap = el("div", "scroll");
        const tb = el("table");
        const thead = el("thead");
        const hr = el("tr");
        for (let i = 0; i < head.length; i++) hr.appendChild(el("th", "", head[i]));
        thead.appendChild(hr);
        tb.appendChild(thead);
        const tbody = el("tbody");
        if (body.length === 0) {
            const tr = el("tr");
            const td = el("td", "muted", L.empty);
            td.setAttribute("colspan", String(head.length));
            tr.appendChild(td);
            tbody.appendChild(tr);
        }
        for (let r = 0; r < body.length; r++) {
            const tr = el("tr", rowCls ? rowCls[r] : "");
            for (let c = 0; c < body[r].length; c++) {
                const cell = body[r][c];
                tr.appendChild(el("td", (cell.num ? "num " : "") + (cell.cls || ""), cell.t));
            }
            tbody.appendChild(tr);
        }
        tb.appendChild(tbody);
        wrap.appendChild(tb);
        return wrap;
    }
    const neg = (n: number | null) => (n !== null && n < 0 ? "neg" : "");

    const root = document.getElementById("app") as HTMLElement;
    const overrides: PnlScenarioOverrides = { driverTrip: null, helperDay: null, maintServiceKm: null, maintTyreKm: null };
    const actual = recompute(D.scenario, {});

    // ── Banners + what-if panel (built once) ──────────────────────────────────────────────────────
    for (let i = 0; i < D.banners.length; i++) root.appendChild(el("p", "banner", D.banners[i]));
    const panel = section(L.whatIfTitle);
    panel.classList.add("panel");
    panel.appendChild(el("p", "muted", L.whatIfHint));
    const grid = el("div", "rates");
    const inputs: HTMLInputElement[] = [];
    for (let i = 0; i < D.rateKeys.length; i++) {
        const rk = D.rateKeys[i];
        const box = el("label", "rate");
        box.appendChild(el("span", "rate-label", rk.label));
        const input = document.createElement("input");
        input.type = "number";
        input.step = "any";
        input.min = "0";
        const base = D.scenario.baseline[rk.key];
        input.value = base === null ? "" : String(base);
        input.addEventListener("input", function () {
            const v = input.value.trim();
            const n = Number(v);
            overrides[rk.key] = v === "" || !isFinite(n) || n < 0 ? null : n;
            render();
        });
        inputs.push(input);
        const row = el("span", "rate-input");
        row.appendChild(input);
        row.appendChild(el("span", "muted", rk.unit));
        box.appendChild(row);
        if (D.scenario.mixed[rk.key]) box.appendChild(el("span", "note", L.mixedRate));
        grid.appendChild(box);
    }
    panel.appendChild(grid);
    const reset = el("button", "btn", L.reset);
    reset.addEventListener("click", function () {
        for (let i = 0; i < D.rateKeys.length; i++) {
            const base = D.scenario.baseline[D.rateKeys[i].key];
            inputs[i].value = base === null ? "" : String(base);
            overrides[D.rateKeys[i].key] = null;
        }
        render();
    });
    panel.appendChild(reset);
    panel.appendChild(el("p", "muted small", L.excludedFixed));
    root.appendChild(panel);

    const dyn = el("div");
    root.appendChild(dyn);

    function render(): void {
        let r: PnlScenarioResult;
        try {
            r = recompute(D.scenario, overrides);
        } catch (e) {
            dyn.replaceChildren(el("p", "banner neg", L.computeError + " " + String(e)));
            return;
        }
        const changed = overrides.driverTrip !== null || overrides.helperDay !== null || overrides.maintServiceKm !== null || overrides.maintTyreKm !== null;
        const s = r.statement;
        dyn.replaceChildren();

        // KPIs
        const kpis = el("div", "kpis");
        const kpiDefs: Array<[string, number | null, number | null, boolean]> = [
            [L.kpiRevenue, s.revenue, actual.statement.revenue, false],
            [L.kpiContribution, s.contribution, actual.statement.contribution, true],
            [L.kpiOperatingProfit, s.operatingProfit, actual.statement.operatingProfit, true],
        ];
        for (let i = 0; i < kpiDefs.length; i++) {
            const k = el("div", "kpi");
            k.appendChild(el("div", "muted small", kpiDefs[i][0]));
            k.appendChild(el("div", "big " + neg(kpiDefs[i][1]), money(kpiDefs[i][1])));
            if (changed && kpiDefs[i][3]) k.appendChild(el("div", "muted small", fill(L.actualValue, { value: money(kpiDefs[i][2]) })));
            kpis.appendChild(k);
        }
        const be = el("div", "kpi");
        be.appendChild(el("div", "muted small", L.kpiBreakEven));
        be.appendChild(el("div", "big", r.breakEven.primaryTrips === null ? "—" : r.breakEven.actualTrips + " / " + r.breakEven.primaryTrips));
        if (changed) be.appendChild(el("div", "muted small", fill(L.actualValue, { value: actual.breakEven.primaryTrips === null ? "—" : String(actual.breakEven.primaryTrips) })));
        kpis.appendChild(be);
        dyn.appendChild(kpis);

        // Month statement + break-even
        const st = section(L.statementTitle);
        const lines: Array<[string, number, string]> = [
            [D.statementLabels.revenue, s.revenue, ""],
            [D.statementLabels.tripFuel, -s.tripFuel, "indent"],
            [D.statementLabels.driverPay, -s.driverPay, "indent"],
            [D.statementLabels.helperShare, -s.helperShare, "indent"],
            [D.statementLabels.maintAccrual, -s.maintAccrual, "indent"],
            [D.statementLabels.contribution, s.contribution, "sub"],
            [D.statementLabels.offPlanFuel, -s.offPlanFuel, "indent"],
            [D.statementLabels.fuelReconciliation, -s.fuelReconciliation, "indent"],
            [D.statementLabels.helperNoTrip, -s.helperNoTrip, "indent"],
            [D.statementLabels.toll, -s.toll, "indent"],
            [D.statementLabels.otherVehicle, -s.otherVehicle, "indent"],
            [D.statementLabels.maintVariance, -s.maintVariance, "indent"],
            [D.statementLabels.afterMonthLines, s.afterMonthLines, "sub"],
            [D.statementLabels.insurance, -s.insurance, "indent"],
            [D.statementLabels.vehicleTax, -s.vehicleTax, "indent"],
        ];
        for (let i = 0; i < D.fixedItems.length; i++) lines.push([D.fixedItems[i].label, -D.fixedItems[i].amount, "indent"]);
        lines.push([D.statementLabels.operatingProfit, s.operatingProfit, "total"]);
        const body: PnlHtmlCell[][] = [];
        const cls: string[] = [];
        for (let i = 0; i < lines.length; i++) {
            body.push([{ t: lines[i][0] }, { t: money(lines[i][1]), num: true, cls: neg(lines[i][1]) }]);
            cls.push(lines[i][2]);
        }
        st.appendChild(table([L.line, L.amount], body, cls));
        const bep = el("p");
        if (r.breakEven.primaryTrips === null) {
            bep.textContent = L.breakEvenTitle + ": " + L.unreachable;
        } else {
            bep.textContent = L.breakEvenTitle + ": " + r.breakEven.primaryTrips + " · " + L.withMonthLines + ": " + r.breakEven.withMonthLinesTrips + " · " + L.actualTrips + ": " + r.breakEven.actualTrips + " · " + fill(L.primaryHint, { fixed: money(s.fixedCosts), avg: money(r.breakEven.avgContribution) });
        }
        st.appendChild(bep);
        st.appendChild(el("p", "muted small", D.subcontractor));
        dyn.appendChild(st);

        // Trucks
        const tsec = section(L.trucksTitle);
        const trows: PnlHtmlCell[][] = [];
        for (let i = 0; i < D.trucks.length; i++) {
            const t = D.trucks[i];
            const rt = r.trucks[i];
            trows.push([
                { t: t.plate }, { t: t.trips, num: true }, { t: money(t.revenue), num: true }, { t: num(t.jobKm, 1), num: true },
                { t: num(t.odoKm, 1), num: true }, { t: num(t.offPlanKm, 1) + " (" + pct(t.offPlanPct) + ")", num: true },
                { t: money(t.fuelRate) + (t.fleetAvg ? " *" : ""), num: true }, { t: money(t.tripFuel), num: true },
                { t: money(t.offPlanFuel), num: true }, { t: money(rt.maintAccrual), num: true }, { t: money(t.maintBills), num: true },
                { t: money(rt.contribution), num: true, cls: neg(rt.contribution) }, { t: money(rt.afterFixed), num: true, cls: neg(rt.afterFixed) },
            ]);
        }
        tsec.appendChild(table([L.colTruck, L.colTrips, L.colRevenue, L.colJobKm, L.colOdoKm, L.colOffPlanKm, L.colFuelRate, L.colTripFuel, L.colOffPlanFuel, L.colMaintAccrual, L.colMaintActual, L.colContribution, L.colAfterFixed], trows));
        tsec.appendChild(el("p", "muted small", L.fleetAvgNote));
        dyn.appendChild(tsec);

        // Lanes
        const lsec = section(L.lanesTitle);
        const lrows: PnlHtmlCell[][] = [];
        for (let i = 0; i < r.lanes.length; i++) {
            const l = r.lanes[i];
            const meta = D.lanes[l.key] || { customer: "", route: l.key, vehicleClass: "" };
            lrows.push([
                { t: meta.customer }, { t: meta.route }, { t: meta.vehicleClass },
                { t: l.trips + " (" + fill(L.primarySupp, { primary: l.primaryTrips, supp: l.supplementaryTrips }) + ")", num: true },
                { t: num(l.jobKmPerTrip, 1), num: true }, { t: money(l.avgRevenue), num: true },
                { t: money(l.avgContribution), num: true, cls: neg(l.avgContribution) }, { t: money(l.contribution), num: true }, { t: pct(l.margin), num: true },
            ]);
        }
        lsec.appendChild(table([L.colCustomer, L.colRoute, L.colVehicleClass, L.colTrips, L.colJobKmPerTrip, L.colAvgRevenue, L.colAvgContribution, L.colTotalContribution, L.colMargin], lrows));
        dyn.appendChild(lsec);

        // Trips
        const psec = section(L.tripsTitle);
        const prow: PnlHtmlCell[][] = [];
        const pcls: string[] = [];
        for (let i = 0; i < D.rows.length; i++) {
            const d = D.rows[i];
            const rr = r.rows[i];
            const badges: string[] = [];
            for (let b = 0; b < d.badges.length; b++) badges.push(d.badges[b].t);
            const cells: PnlHtmlCell[] = [];
            for (let c = 0; c < d.cells.length; c++) cells.push({ t: d.cells[c], num: c >= 6 });
            cells.push({ t: money(rr.driverPay), num: true }, { t: money(rr.helperShare), num: true }, { t: money(rr.maintAccrual), num: true });
            cells.push({ t: money(rr.contribution), num: true, cls: neg(rr.contribution) }, { t: pct(rr.margin), num: true }, { t: badges.join(", ") });
            prow.push(cells);
            pcls.push(d.counted ? "" : "excluded");
        }
        psec.appendChild(table([L.colDate, L.colTruck, L.colDriver, L.colCustomer, L.colRoute, L.colJobCategory, L.colJobKm, L.colRevenue, L.colFuel, L.colDriverPay, L.colHelper, L.colMaintAccrual, L.colContribution, L.colMargin, L.colBadges], prow, pcls));
        dyn.appendChild(psec);

        // Warnings + settings used
        if (D.warnings.length > 0) {
            const wsec = section(L.warningsTitle);
            const ul = el("ul");
            for (let i = 0; i < D.warnings.length; i++) {
                const li = el("li", D.warnings[i].bad ? "neg" : "", D.warnings[i].t);
                if (D.warnings[i].refs) li.appendChild(el("span", "muted small", " " + D.warnings[i].refs));
                ul.appendChild(li);
            }
            wsec.appendChild(ul);
            dyn.appendChild(wsec);
        }
        const ssec = section(L.settingsTitle);
        const sul = el("ul");
        for (let i = 0; i < D.settingsUsed.length; i++) sul.appendChild(el("li", "", D.settingsUsed[i]));
        ssec.appendChild(sul);
        dyn.appendChild(ssec);
    }

    render();
}
