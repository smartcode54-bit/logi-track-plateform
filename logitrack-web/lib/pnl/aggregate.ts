/** Lane roll-up, break-even and the warning bag for the P&L (spec R17, R18, R20). */
import { round2 } from "./settings";
import type { PnlBreakEven, PnlLaneRow, PnlTripRow, PnlWarning, PnlWarningCode } from "./types";

export function laneKeyFor(row: Pick<PnlTripRow, "kind" | "customerId" | "originCode" | "destinationCodes" | "vehicleClass">): string {
    const customer = row.customerId ?? "-";
    if (row.kind === "standby") return `${customer}|STANDBY`;
    return [customer, row.originCode ?? "?", row.destinationCodes.join("+") || "?", row.vehicleClass ?? "-"].join("|");
}

/** Counted rows only — a lane's average must not be dragged by rows that are not in the totals. */
export function buildLanes(rows: PnlTripRow[]): PnlLaneRow[] {
    const lanes = new Map<string, PnlLaneRow & { jobKmSum: number }>();
    for (const r of rows) {
        if (!r.counted) continue;
        const lane =
            lanes.get(r.laneKey) ??
            {
                key: r.laneKey,
                isStandby: r.kind === "standby",
                customerId: r.customerId,
                customerName: r.customerName,
                originCode: r.kind === "standby" ? undefined : r.originCode,
                destinationCodes: r.kind === "standby" ? [] : r.destinationCodes,
                vehicleClass: r.kind === "standby" ? undefined : r.vehicleClass,
                trips: 0,
                primaryTrips: 0,
                supplementaryTrips: 0,
                jobKmPerTrip: 0,
                jobKmSum: 0,
                revenueThb: 0,
                contributionThb: 0,
                avgRevenueThb: 0,
                avgContributionThb: 0,
                marginPct: null,
            };
        lane.trips++;
        if (r.jobCategory === "SUPPLEMENTARY") lane.supplementaryTrips++;
        else lane.primaryTrips++;
        lane.jobKmSum += r.jobKm ?? 0;
        lane.revenueThb = round2(lane.revenueThb + (r.revenueThb ?? 0));
        lane.contributionThb = round2(lane.contributionThb + (r.contributionThb ?? 0));
        lanes.set(r.laneKey, lane);
    }
    return [...lanes.values()]
        .map(({ jobKmSum, ...lane }) => ({
            ...lane,
            jobKmPerTrip: round2(jobKmSum / lane.trips),
            avgRevenueThb: round2(lane.revenueThb / lane.trips),
            avgContributionThb: round2(lane.contributionThb / lane.trips),
            marginPct: lane.revenueThb > 0 ? round2((lane.contributionThb / lane.revenueThb) * 100) : null,
        }))
        .sort((a, b) => b.avgContributionThb - a.avgContributionThb);
}

/** ceil with a tolerance so 175.0000000001 (float noise) does not become 176. */
const ceilTrips = (n: number) => Math.max(0, Math.ceil(n - 1e-9));

export function computeBreakEven(params: {
    contributionThb: number;
    countedUnits: number;
    fixedCostsThb: number;
    monthLinesThb: number;
}): PnlBreakEven {
    const { contributionThb, countedUnits, fixedCostsThb, monthLinesThb } = params;
    const avg = countedUnits > 0 ? contributionThb / countedUnits : null;
    const reachable = avg !== null && avg > 0;
    return {
        primaryTrips: reachable ? ceilTrips(fixedCostsThb / avg) : null,
        withMonthLinesTrips: reachable ? ceilTrips((fixedCostsThb + monthLinesThb) / avg) : null,
        actualTrips: countedUnits,
        avgContributionThb: avg === null ? null : round2(avg),
    };
}

/** Collects fail-loud findings by code (spec §4.5). */
export class WarningBag {
    private readonly map = new Map<PnlWarningCode, PnlWarning>();

    add(code: PnlWarningCode, ref?: string, amountThb?: number): void {
        const w = this.map.get(code) ?? { code, count: 0, refs: [] };
        w.count++;
        if (ref && !w.refs.includes(ref)) w.refs.push(ref);
        if (amountThb !== undefined && Number.isFinite(amountThb)) {
            w.amountThb = round2((w.amountThb ?? 0) + amountThb);
        }
        this.map.set(code, w);
    }

    addCount(code: PnlWarningCode, count: number, amountThb?: number): void {
        if (count <= 0) return;
        const w = this.map.get(code) ?? { code, count: 0, refs: [] };
        w.count += count;
        if (amountThb !== undefined) w.amountThb = round2((w.amountThb ?? 0) + amountThb);
        this.map.set(code, w);
    }

    list(): PnlWarning[] {
        return [...this.map.values()];
    }
}
