"use client";

/**
 * Export a computed P&L month as ONE self-contained HTML file in which the reader can change the
 * four P&L rates and see every rate-dependent figure recompute (spec R21, owner 2026-11-16). Replaces
 * the Excel export. The data is frozen at export; nothing is written back to the system.
 *
 * Both scripts in the file come from typed, tested functions via `toString()`:
 *   - `recomputePnlScenario` (lib/pnl/scenario.ts) — the rate maths, checked against computePnl
 *   - `pnlHtmlClient` (./pnlHtmlClient.ts) — the renderer, textContent only
 */
import { activeFixedCostVersion, buildPnlScenario, recomputePnlScenario, type PnlResult } from "@/lib/pnlCompute";
import type { PnlFixedCostVersion, PnlRateRow } from "@/validate/pnlSettingsSchema";
import { EXCLUDING_CODES, fixedCategoryLabel, fmtNum, fmtThb, routeLabel } from "./format";
import { pnlHtmlClient, type PnlHtmlData } from "./pnlHtmlClient";

type Translate = (key: string, params?: Record<string, string | number>) => string;

export interface PnlHtmlParams {
    result: PnlResult;
    hubLabels: Record<string, string>;
    rates: PnlRateRow[];
    fixedCosts: PnlFixedCostVersion[];
    t: Translate;
    language: string;
    now?: Date;
}

/** JSON safe inside <script>: no "</script>", "<!--" or line separators can close or break it. */
export function jsonForScript(value: unknown): string {
    return JSON.stringify(value)
        .replace(/</g, "\\u003c")
        .replace(/>/g, "\\u003e")
        .replace(/&/g, "\\u0026")
        .split(String.fromCharCode(0x2028)).join("\\" + "u2028")
        .split(String.fromCharCode(0x2029)).join("\\" + "u2029");
}

function escapeHtml(s: string): string {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

export function buildPnlHtmlData(p: PnlHtmlParams): PnlHtmlData {
    const { result, hubLabels, rates, fixedCosts, t } = p;
    const s = result.statement;
    const scenario = buildPnlScenario(result, rates);
    const keyLabels: Array<[string, string]> = [
        ["empty", "pnl.empty"], ["whatIfTitle", "pnl.html.whatIfTitle"], ["whatIfHint", "pnl.html.whatIfHint"],
        ["reset", "pnl.html.reset"], ["mixedRate", "pnl.html.mixedRate"], ["excludedFixed", "pnl.html.excludedFixed"],
        ["actualValue", "pnl.html.actualValue"], ["computeError", "pnl.html.computeError"], ["fleetAvgNote", "pnl.html.fleetAvgNote"],
        ["kpiRevenue", "pnl.kpi.revenue"], ["kpiContribution", "pnl.kpi.contribution"], ["kpiOperatingProfit", "pnl.kpi.operatingProfit"],
        ["kpiBreakEven", "pnl.kpi.breakEven"], ["statementTitle", "pnl.month.statementTitle"], ["line", "pnl.html.line"],
        ["amount", "pnl.html.amount"], ["breakEvenTitle", "pnl.breakEven.title"], ["unreachable", "pnl.breakEven.unreachable"],
        ["withMonthLines", "pnl.breakEven.withMonthLines"], ["actualTrips", "pnl.breakEven.actual"], ["primaryHint", "pnl.breakEven.primaryHint"],
        ["trucksTitle", "pnl.tabs.trucks"], ["lanesTitle", "pnl.tabs.lanes"], ["tripsTitle", "pnl.tabs.trips"],
        ["warningsTitle", "pnl.html.warningsTitle"], ["settingsTitle", "pnl.html.settingsTitle"], ["primarySupp", "pnl.lane.primarySupp"],
        ["colTruck", "pnl.col.truck"], ["colTrips", "pnl.col.trips"], ["colRevenue", "pnl.col.revenue"], ["colJobKm", "pnl.col.jobKm"],
        ["colOdoKm", "pnl.col.odoKm"], ["colOffPlanKm", "pnl.col.offPlanKm"], ["colFuelRate", "pnl.col.fuelRate"],
        ["colTripFuel", "pnl.col.tripFuel"], ["colOffPlanFuel", "pnl.line.offPlanFuel"], ["colMaintAccrual", "pnl.col.maintAccrual"],
        ["colMaintActual", "pnl.col.maintActual"], ["colContribution", "pnl.trip.contribution"], ["colAfterFixed", "pnl.col.afterFixed"],
        ["colCustomer", "pnl.col.customer"], ["colRoute", "pnl.col.route"], ["colVehicleClass", "pnl.col.vehicleClass"],
        ["colJobKmPerTrip", "pnl.col.jobKmPerTrip"], ["colAvgRevenue", "pnl.col.avgRevenue"], ["colAvgContribution", "pnl.col.avgContribution"],
        ["colTotalContribution", "pnl.col.totalContribution"], ["colMargin", "pnl.col.margin"], ["colDate", "pnl.col.date"],
        ["colDriver", "pnl.col.driver"], ["colJobCategory", "pnl.col.jobCategory"], ["colFuel", "pnl.trip.fuel"],
        ["colDriverPay", "pnl.trip.driverPay"], ["colHelper", "pnl.trip.helperShare"], ["colBadges", "pnl.col.badges"],
    ];
    const labels: Record<string, string> = {};
    for (const [k, key] of keyLabels) labels[k] = t(key);

    const unit = (k: string) => t(k === "driverTrip" ? "pnl.settings.unit.trip" : k === "helperDay" ? "pnl.settings.unit.helperDay" : "pnl.settings.unit.km");
    const rateKeys: PnlHtmlData["rateKeys"] = (
        [["driverTrip", "driver_trip"], ["helperDay", "helper_day"], ["maintServiceKm", "maint_service_km"], ["maintTyreKm", "maint_tyre_km"]] as const
    ).map(([key, kind]) => ({ key, label: t(`pnl.settings.rate.${kind}`), unit: unit(key) }));

    const banners = [t("pnl.html.frozenNote")];
    if (result.flags.monthInProgress) banners.push(t("pnl.banner.monthInProgress"));
    if (result.flags.hasPlanBasisCustomer) banners.push(t("pnl.banner.planBasis"));
    if (s.excludedCount > 0) banners.push(t("pnl.warn.excludedSummary", { count: s.excludedCount, amount: fmtThb(s.excludedRevenueThb) }));

    const rows: PnlHtmlData["rows"] = result.trips.map((r) => {
        const prefix = r.kind === "standby" ? `${t("pnl.kind.standby")} · ` : r.isMultiDrop ? `${t("pnl.kind.multiDrop")} · ` : "";
        return {
            counted: r.counted,
            cells: [
                r.dateKey, r.truckPlate ?? "—", r.driverName ?? "—", r.customerName ?? "—",
                prefix + routeLabel(r.originCode, r.destinationCodes, hubLabels),
                r.jobCategory ? t(`pnl.jobCategory.${r.jobCategory}`) : "—",
                fmtNum(r.jobKm), fmtThb(r.revenueThb), fmtThb(r.fuelThb),
            ],
            badges: [
                ...r.reasons.map((c) => ({ t: t(`pnl.badge.${c}`) + (c === "noJobDistance" && r.missingLeg ? ` (${r.missingLeg.join("→")})` : ""), bad: true })),
                ...r.notes.map((c) => ({ t: t(`pnl.badge.${c}`), bad: false })),
            ],
        };
    });

    const lanes: PnlHtmlData["lanes"] = {};
    for (const l of result.lanes) {
        lanes[l.key] = {
            customer: l.customerName ?? "—",
            route: l.isStandby ? t("pnl.kind.standby") : routeLabel(l.originCode, l.destinationCodes, hubLabels),
            vehicleClass: l.vehicleClass ?? "—",
        };
    }

    const trucks: PnlHtmlData["trucks"] = result.trucks.map((tr) => ({
        plate: tr.plate,
        trips: tr.standbys > 0 ? `${tr.trips} + ${tr.standbys}` : String(tr.trips),
        revenue: tr.revenueThb, jobKm: tr.jobKm, odoKm: tr.odoKm, offPlanKm: tr.offPlanKm, offPlanPct: tr.offPlanPct,
        fuelRate: tr.fuelRateThbPerKm, fleetAvg: tr.fuelRateIsFleetAvg, tripFuel: tr.tripFuelThb, offPlanFuel: tr.offPlanFuelThb,
        maintBills: tr.maintActualPmThb + tr.maintActualCmThb,
    }));

    const statementLabels: Record<string, string> = {
        revenue: t("pnl.line.revenue", { trips: s.countedTrips, standbys: s.countedStandbys }),
        tripFuel: t("pnl.line.tripFuel"), driverPay: t("pnl.line.driverPay"), helperShare: t("pnl.line.helperShare"),
        maintAccrual: t("pnl.line.maintAccrual"), contribution: t("pnl.line.contribution"), offPlanFuel: t("pnl.line.offPlanFuel"),
        fuelReconciliation: t("pnl.line.fuelReconciliation", { cash: fmtThb(s.fuelCashThb) }), helperNoTrip: t("pnl.line.helperNoTrip"),
        toll: t("pnl.line.toll"), otherVehicle: t("pnl.line.otherVehicleExpenses"),
        maintVariance: t("pnl.line.maintenanceVariance", { actual: fmtThb(s.maintActualThb) }),
        afterMonthLines: t("pnl.line.afterMonthLines"), insurance: t("pnl.line.insurance"), vehicleTax: t("pnl.line.vehicleTax"),
        operatingProfit: t("pnl.line.operatingProfit"),
    };
    const fixedItems = s.fixedItemLines.map((l) => ({ label: fixedCategoryLabel(l.category, l.label, t), amount: l.amountThb }));

    const byItem = new Map<string, PnlFixedCostVersion[]>();
    for (const v of fixedCosts) byItem.set(v.itemId, [...(byItem.get(v.itemId) ?? []), v]);
    const settingsUsed: string[] = [];
    for (const versions of byItem.values()) {
        const v = activeFixedCostVersion(versions, result.monthKey);
        if (!v || v.isStop || (v.effectiveToMonth && v.effectiveToMonth < result.monthKey)) continue;
        const monthly = v.frequency === "yearly" ? v.amountThb / 12 : v.amountThb;
        settingsUsed.push(
            `${v.name} (${fixedCategoryLabel(v.category, v.categoryLabel, t)}${v.truckPlate ? ` · ${v.truckPlate}` : ""}) — ${fmtThb(monthly)} ${t("pnl.html.perMonth")}`,
        );
    }
    settingsUsed.push(t("pnl.settings.fixed.renewalNote"));

    const warnings = [...result.warnings]
        .sort((a, b) => Number(EXCLUDING_CODES.includes(b.code)) - Number(EXCLUDING_CODES.includes(a.code)))
        .map((w) => ({
            t: t(`pnl.warn.${w.code}`, { count: w.count, amount: w.amountThb !== undefined ? fmtThb(w.amountThb) : "—" }),
            refs: w.refs.slice(0, 10).join(", ") + (w.refs.length > 10 ? ` +${w.refs.length - 10}` : ""),
            bad: EXCLUDING_CODES.includes(w.code),
        }));

    return {
        scenario, labels, rateKeys, banners, rows, lanes, trucks, statementLabels, fixedItems,
        subcontractor: `${t("pnl.month.subcontractorTitle")}: ${t("pnl.month.subcontractorTrips", { count: result.subcontractor.trips })} · ${t("pnl.month.subcontractorRevenue", { amount: fmtThb(result.subcontractor.revenueThb) })} — ${t("pnl.month.subcontractorNote")}`,
        warnings, settingsUsed,
    };
}

const CSS = `
:root{--fg:#111827;--muted:#6b7280;--line:#e5e7eb;--bg:#fff;--card:#f9fafb;--neg:#b91c1c;--accent:#1e50a0}
*{box-sizing:border-box}body{margin:0;padding:24px;font:14px/1.5 "Sarabun","Noto Sans Thai","Segoe UI",system-ui,sans-serif;color:var(--fg);background:var(--bg)}
h1{margin:0 0 4px;font-size:22px}h2{margin:0 0 12px;font-size:16px}.muted{color:var(--muted)}.small{font-size:12px}
.card{border:1px solid var(--line);border-radius:8px;padding:16px;margin:16px 0;background:var(--bg)}
.panel{background:var(--card);border-color:var(--accent)}.banner{margin:6px 0;padding:8px 12px;border-radius:6px;background:#eff6ff;color:#1e3a8a}
.rates{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:12px;margin:12px 0}
.rate{display:flex;flex-direction:column;gap:4px}.rate-label{font-weight:600}.rate-input{display:flex;align-items:center;gap:6px}
.rate input{width:120px;padding:6px 8px;border:1px solid #9ca3af;border-radius:6px;font:inherit}.note{font-size:12px;color:#92400e}
.btn{padding:6px 14px;border:1px solid var(--accent);background:var(--accent);color:#fff;border-radius:6px;font:inherit;cursor:pointer}
.kpis{display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:12px;margin:16px 0}
.kpi{border:1px solid var(--line);border-radius:8px;padding:12px}.big{font-size:20px;font-weight:700}
.scroll{overflow-x:auto}table{border-collapse:collapse;width:100%;font-size:13px}th,td{border-bottom:1px solid var(--line);padding:6px 8px;text-align:left;white-space:nowrap}
th{background:var(--card);position:sticky;top:0}td.num{text-align:right;font-variant-numeric:tabular-nums}
tr.sub td{font-weight:600;background:var(--card)}tr.total td{font-weight:700;font-size:15px;background:#e0e7ff}tr.indent td:first-child{padding-left:28px}
tr.excluded td{background:#fffbeb}.neg{color:var(--neg)}
@media print{.panel input{border:none}.btn{display:none}body{padding:0}}
`;

export function buildPnlHtml(p: PnlHtmlParams): string {
    const data = buildPnlHtmlData(p);
    const now = p.now ?? new Date();
    const title = `${p.t("pnl.title")} — ${p.result.monthKey}`;
    return [
        "<!DOCTYPE html>",
        `<html lang="${escapeHtml(p.language)}">`,
        "<head>",
        '<meta charset="utf-8">',
        '<meta name="viewport" content="width=device-width, initial-scale=1">',
        `<title>${escapeHtml(title)}</title>`,
        `<style>${CSS}</style>`,
        "</head>",
        "<body>",
        `<h1>${escapeHtml(title)}</h1>`,
        `<p class="muted small">${escapeHtml(p.t("pnl.html.generatedAt", { date: now.toLocaleString(p.language === "th" ? "th-TH" : "en-GB") }))}</p>`,
        '<div id="app"></div>',
        `<script id="pnl-data" type="application/json">${jsonForScript(data)}</script>`,
        "<script>",
        `var recomputePnlScenario = (${recomputePnlScenario.toString()});`,
        `var pnlHtmlClient = (${pnlHtmlClient.toString()});`,
        'pnlHtmlClient(JSON.parse(document.getElementById("pnl-data").textContent), recomputePnlScenario);',
        "</script>",
        "</body>",
        "</html>",
    ].join("\n");
}

/** Build the file and hand it to the browser as a download. */
export function downloadPnlHtml(p: PnlHtmlParams): void {
    const now = p.now ?? new Date();
    const html = buildPnlHtml({ ...p, now });
    const blob = new Blob([html], { type: "text/html;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `PnL_${p.result.monthKey}_${now.toISOString().slice(0, 16).replace(/[-:T]/g, "")}.html`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
}
